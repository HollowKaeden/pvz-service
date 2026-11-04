//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	trmpgx "github.com/avito-tech/go-transaction-manager/drivers/pgxv5/v2"
	"github.com/avito-tech/go-transaction-manager/trm/v2/manager"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/events"
	"github.com/HollowKaeden/pvz-service/internal/eventschema"
	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/service"
)

// abortTx выполняет всё тело транзакции, включая INSERT outbox, но затем
// откатывает её. Проверяется настоящая атомарность в Postgres.
type abortTx struct{ tx service.TxManager }

var errAbort = errors.New("откат после записи события")

func (tx abortTx) Do(ctx context.Context, fn func(context.Context) error) error {
	return tx.tx.Do(ctx, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		return errAbort
	})
}

func outboxParts(t *testing.T) (*manager.Manager, *postgres.OutboxRepository) {
	t.Helper()
	tx, err := manager.New(trmpgx.NewDefaultFactory(pool))
	require.NoError(t, err)
	return tx, postgres.NewOutboxRepository(postgres.NewDB(pool))
}

func TestOutboxRollback(t *testing.T) {
	resetDB(t)
	ctx := t.Context()
	p, err := services.PVZ.Create(ctx, "Казань")
	require.NoError(t, err)
	reception, err := services.Receptions.Open(ctx, p.ID)
	require.NoError(t, err)
	_, err = services.Products.Add(ctx, p.ID, "обувь")
	require.NoError(t, err)
	tx, outbox := outboxParts(t)
	db := postgres.NewDB(pool)
	s := service.NewReception(abortTx{tx}, postgres.NewPVZRepository(db), postgres.NewReceptionRepository(db),
		postgres.NewProductRepository(db), outbox, recorder)

	_, err = s.Close(ctx, p.ID)
	require.ErrorIs(t, err, errAbort)
	n, err := outbox.Pending(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
	// После отката приёмка осталась открытой и закрывается обычным сервисом.
	closed, err := services.Receptions.Close(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, reception.ID, closed.ID)
	n, err = outbox.Pending(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}

func TestOutboxLockAndCleanup(t *testing.T) {
	resetDB(t)
	ctx := t.Context()
	tx, outbox := outboxParts(t)
	for range 3 {
		require.NoError(t, outbox.Add(ctx, eventschema.Event{
			ID: uuid.New(), Type: eventschema.ReceptionClosedType, Version: 1,
			Payload: eventschema.ReceptionClosed{PVZID: uuid.New()},
		}))
	}
	require.NoError(t, tx.Do(ctx, func(ctx context.Context) error {
		first, found, err := outbox.Next(ctx)
		require.NoError(t, err)
		require.True(t, found)
		// Другая транзакция не ждёт заблокированное сообщение.
		//nolint:contextcheck // Нужен отдельный контекст: ctx передал бы в менеджер уже открытую транзакцию.
		return tx.Do(t.Context(), func(other context.Context) error {
			second, found, err := outbox.Next(other)
			require.NoError(t, err)
			require.True(t, found)
			assert.Greater(t, second.ID, first.ID)
			return outbox.MarkSent(other, second.ID)
		})
	}))
	// Первая запись всё ещё неотправленная, вторая недавно отправлена,
	// третья отправлена двое суток назад. Удалиться должна только третья.
	_, err := pool.Exec(ctx, `UPDATE outbox SET sent_at = now() - interval '2 days' WHERE id = (SELECT max(id) FROM outbox)`)
	require.NoError(t, err)
	require.NoError(t, outbox.DeleteSent(ctx, time.Now().Add(-24*time.Hour)))
	var total int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&total))
	assert.Equal(t, 2, total)
	n, err := outbox.Pending(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}

// Реальная Kafka того же образа, что в compose. Внешний адрес становится
// известен после старта контейнера, поэтому запуск брокера ждёт скрипт.
func startKafka(t *testing.T) (testcontainers.Container, string) {
	t.Helper()
	ctx := t.Context()
	// Docker выбирает новый случайный host-порт после Stop/Start. Для
	// проверки восстановления того же producer нужен неизменный адрес.
	reservation, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, hostPort, err := net.SplitHostPort(reservation.Addr().String())
	require.NoError(t, err)
	require.NoError(t, reservation.Close())
	containerPort, err := network.ParsePort("9092/tcp")
	require.NoError(t, err)
	ctr, err := testcontainers.Run(ctx, "apache/kafka:4.1.1",
		testcontainers.WithExposedPorts("9092/tcp"),
		testcontainers.WithHostConfigModifier(func(cfg *container.HostConfig) {
			cfg.PortBindings = network.PortMap{containerPort: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: hostPort}}}
		}),
		testcontainers.WithEntrypoint("sh", "-c"),
		testcontainers.WithCmd("while [ ! -f /tmp/start-kafka.sh ]; do sleep 0.1; done; exec sh /tmp/start-kafka.sh"),
		testcontainers.WithEnv(map[string]string{
			"KAFKA_NODE_ID": "1", "KAFKA_PROCESS_ROLES": "broker,controller",
			"KAFKA_LISTENERS":                        "PLAINTEXT://:9092,CONTROLLER://:9093",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":   "PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT",
			"KAFKA_INTER_BROKER_LISTENER_NAME":       "PLAINTEXT",
			"KAFKA_CONTROLLER_LISTENER_NAMES":        "CONTROLLER",
			"KAFKA_CONTROLLER_QUORUM_VOTERS":         "1@localhost:9093",
			"CLUSTER_ID":                             "4L6g3nShT-eMCtK--X86sw",
			"KAFKA_HEAP_OPTS":                        "-Xms256m -Xmx256m",
			"KAFKA_AUTO_CREATE_TOPICS_ENABLE":        "false",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR": "1",
			"KAFKA_OFFSETS_TOPIC_NUM_PARTITIONS":     "1",
		}),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		logs, err := ctr.Logs(logCtx)
		if err != nil {
			t.Log("журнал Kafka:", err)
			return
		}
		defer logs.Close()
		body, _ := io.ReadAll(logs)
		if len(body) > 4000 {
			body = body[len(body)-4000:]
		}
		t.Log(string(body))
	})
	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "9092/tcp")
	require.NoError(t, err)
	broker := net.JoinHostPort(host, port.Port())
	script := fmt.Sprintf("#!/bin/sh\nexport KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://%s\nexec /etc/kafka/docker/run\n", broker)
	require.NoError(t, ctr.CopyToContainer(ctx, []byte(script), "/tmp/start-kafka.sh", 0o644))
	require.NoError(t, wait.ForLog("Kafka Server started").WithStartupTimeout(90*time.Second).WaitUntilReady(ctx, ctr))
	return ctr, broker
}

func TestOutboxKafkaRecovery(t *testing.T) {
	resetDB(t)
	ctx := t.Context()
	ctr, broker := startKafka(t)
	topic := "pvz.reception.closed"
	publisher, err := events.NewKafka([]string{broker}, topic)
	require.NoError(t, err)
	defer publisher.Close()
	tx, outbox := outboxParts(t)
	relay := events.NewRelay(tx, outbox, publisher, recorder, slog.New(slog.DiscardHandler))
	m, e := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	pvz := createPVZ(t, m, "Москва")
	closeReception := func() {
		mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", e, map[string]any{"pvzId": pvz}, nil)
		mustCall(t, http.StatusCreated, http.MethodPost, "/products", e, map[string]any{"pvzId": pvz, "type": "обувь"}, nil)
		mustCall(t, http.StatusCreated, http.MethodPost, "/products", e, map[string]any{"pvzId": pvz, "type": "электроника"}, nil)
		mustCall(t, http.StatusOK, http.MethodPost, "/pvz/"+pvz.String()+"/delete_last_product", e, nil, nil)
		mustCall(t, http.StatusOK, http.MethodPost, "/pvz/"+pvz.String()+"/close_last_reception", e, nil, nil)
	}
	closeReception()
	// Kafka подтвердила запись, но коммит outbox не прошёл. Повторная
	// доставка должна нести тот же UUID, чтобы billing мог убрать дубль.
	failedCommit := events.NewRelay(abortTx{tx}, outbox, publisher, recorder, slog.New(slog.DiscardHandler))
	sent, err := failedCommit.PublishNext(ctx)
	require.ErrorIs(t, err, errAbort)
	assert.False(t, sent)
	sent, err = relay.PublishNext(ctx)
	require.NoError(t, err)
	require.True(t, sent)

	// Брокер останавливается после первой успешной доставки: проверяется
	// обрыв уже установленного соединения, а не только недоступный адрес.
	stopTimeout := 5 * time.Second
	require.NoError(t, ctr.Stop(ctx, &stopTimeout))
	closeReception() // HTTP API продолжает отвечать без Kafka.
	sent, err = relay.PublishNext(ctx)
	require.Error(t, err)
	assert.False(t, sent)
	n, err := outbox.Pending(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	require.NoError(t, ctr.Start(ctx))
	newPort, err := ctr.MappedPort(ctx, "9092/tcp")
	require.NoError(t, err)
	_, oldPort, err := net.SplitHostPort(broker)
	require.NoError(t, err)
	require.Equal(t, oldPort, newPort.Port())
	var deliveryErr error
	restored := assert.Eventually(t, func() bool {
		sent, err := relay.PublishNext(ctx)
		deliveryErr = err
		return err == nil && sent
	}, 45*time.Second, time.Second)
	require.NoError(t, deliveryErr)
	require.True(t, restored)
	n, err = outbox.Pending(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)

	consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)
	defer consumer.Close()
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	seen := map[uuid.UUID]int{}
	bodies := map[uuid.UUID]string{}
	records := 0
	var partition int32 = -1
	for (len(seen) < 2 || records < 3) && readCtx.Err() == nil {
		fetches := consumer.PollFetches(readCtx)
		require.NoError(t, fetches.Err())
		for _, record := range fetches.Records() {
			assert.Equal(t, pvz.String(), string(record.Key))
			if partition < 0 {
				partition = record.Partition
			}
			assert.Equal(t, partition, record.Partition, "события ПВЗ в одной партиции")
			var event eventschema.Event
			require.NoError(t, json.Unmarshal(record.Value, &event))
			assert.Equal(t, eventschema.ReceptionClosedType, event.Type)
			assert.Equal(t, eventschema.Version, event.Version)
			assert.Equal(t, pvz, event.Payload.PVZID)
			assert.Equal(t, "Москва", event.Payload.City)
			assert.Equal(t, event.OccurredAt, event.Payload.ClosedAt)
			assert.False(t, event.Payload.ClosedAt.Before(event.Payload.OpenedAt))
			require.Len(t, event.Payload.Products, 1, "удалённого товара в событии нет")
			assert.Equal(t, "обувь", event.Payload.Products[0].Type)
			if previous, ok := bodies[event.ID]; ok {
				assert.Equal(t, previous, string(record.Value), "повтор сохраняет весь JSON события")
			}
			bodies[event.ID] = string(record.Value)
			seen[event.ID]++
			records++
		}
	}
	require.Len(t, seen, 2)
	assert.GreaterOrEqual(t, records, 3)
}
