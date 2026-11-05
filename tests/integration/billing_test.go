//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	trmpgx "github.com/avito-tech/go-transaction-manager/drivers/pgxv5/v2"
	"github.com/avito-tech/go-transaction-manager/trm/v2/manager"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	bc "github.com/HollowKaeden/pvz-service/internal/billing/consumer"
	bm "github.com/HollowKaeden/pvz-service/internal/billing/metrics"
	bp "github.com/HollowKaeden/pvz-service/internal/billing/repository/postgres"
	bs "github.com/HollowKaeden/pvz-service/internal/billing/service"
	bhttp "github.com/HollowKaeden/pvz-service/internal/billing/transport/http"
	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/events"
	"github.com/HollowKaeden/pvz-service/internal/eventschema"
)

func billingPool(t *testing.T, database string) *pgxpool.Pool {
	t.Helper()
	_, err := pool.Exec(t.Context(), `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()+` OWNER pvz`)
	require.NoError(t, err)
	dbURL, err := url.Parse(pool.Config().ConnString())
	require.NoError(t, err)
	dbURL.Path = "/" + database
	bpool, err := bp.NewPool(t.Context(), dbURL.String(), 10)
	require.NoError(t, err)
	t.Cleanup(bpool.Close)
	require.NoError(t, bp.Migrate(t.Context(), bpool))
	require.NoError(t, bp.Migrate(t.Context(), bpool))
	return bpool
}

func TestBilling(t *testing.T) {
	resetDB(t)
	ctx := t.Context()
	log := slog.New(slog.DiscardHandler)
	bpool := billingPool(t, "billing_test")
	repo := bp.New(bpool)
	tx, err := manager.New(trmpgx.NewDefaultFactory(bpool))
	require.NoError(t, err)
	m := bm.New()
	svc, err := bs.New(ctx, tx, repo, m)
	require.NoError(t, err)
	unlock, err := bp.Lock(ctx, bpool)
	require.NoError(t, err)
	_, err = bp.Lock(ctx, bpool)
	require.ErrorContains(t, err, "остановите consumer")
	unlock()
	unlock, err = bp.Lock(ctx, bpool)
	require.NoError(t, err)
	unlock()

	// Настоящий HTTP закрывает приёмку; снимок из outbox — вход billing.
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	id := createPVZ(t, moderator, "Москва")
	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee, map[string]any{"pvzId": id}, nil)
	for _, kind := range []string{"электроника", "одежда", "обувь"} {
		mustCall(t, http.StatusCreated, http.MethodPost, "/products", employee, map[string]any{"pvzId": id, "type": kind}, nil)
	}
	mustCall(t, http.StatusOK, http.MethodPost, "/pvz/"+id.String()+"/close_last_reception", employee, nil, nil)
	var data []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT body FROM outbox WHERE pvz_id=$1`, id).Scan(&data))
	var event eventschema.Event
	require.NoError(t, json.Unmarshal(data, &event))

	t.Run("rollback and concurrent duplicate", func(t *testing.T) {
		broken, err := bs.New(ctx, abortTx{tx}, repo, m)
		require.NoError(t, err)
		require.ErrorIs(t, broken.Apply(ctx, data), errAbort)
		items, err := repo.Balances(ctx, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, items)
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { assert.NoError(t, svc.Apply(ctx, data)) })
		}
		wg.Wait()
		items, err = repo.Balances(ctx, 10, 0)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.EqualValues(t, 18000, items[0].Amount)
		journal, err := repo.Journal(ctx, id, 30, 0)
		require.NoError(t, err)
		require.Len(t, journal, 1)
		assert.Equal(t, event.ID, journal[0].EventID)
	})

	// Индекс проверяется на объёме, где последовательное чтение уже невыгодно.
	t.Run("journal explain", func(t *testing.T) {
		_, err := bpool.Exec(ctx, `INSERT INTO payouts SELECT gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),'Казань',3000,1,now(),now() FROM generate_series(1,10000); ANALYZE payouts`)
		require.NoError(t, err)
		rows, err := bpool.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS) SELECT event_id,reception_id,pvz_id,city,amount_kopecks,products,closed_at,processed_at FROM payouts WHERE pvz_id=$1 ORDER BY processed_at DESC,event_id DESC LIMIT 30`, id)
		require.NoError(t, err)
		var plan strings.Builder
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan.WriteString(line + "\n")
		}
		require.NoError(t, rows.Err())
		rows.Close()
		t.Log(plan.String())
		assert.Contains(t, plan.String(), "payouts_pvz_processed_idx")
		_, err = bpool.Exec(ctx, `TRUNCATE payouts,balances`)
		require.NoError(t, err)
	})

	_, broker := startKafka(t)
	topic, group := "billing-test", "billing-test"
	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, bc.EnsureTopics(ctx, client, topic))
	start := func() func() {
		worker, err := bc.New([]string{broker}, topic, group, svc, m, log)
		require.NoError(t, err)
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- worker.Run(workerCtx) }()
		return func() { cancel(); require.NoError(t, <-done); worker.Close() }
	}
	var stop func()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	balance := func(amount int64) bool {
		items, err := repo.Balances(ctx, 10, 0)
		return err == nil && len(items) == 1 && items[0].Amount == amount
	}
	publish := func(value []byte) {
		require.NoError(t, client.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(id.String()), Value: value}).FirstErr())
	}

	t.Run("close outbox Kafka payout and duplicate", func(t *testing.T) {
		stop = start()
		publisher, err := events.NewKafka([]string{broker}, topic)
		require.NoError(t, err)
		defer publisher.Close()
		pvzTx, outbox := outboxParts(t)
		relay := events.NewRelay(pvzTx, outbox, publisher, recorder, log)
		sent, err := relay.PublishNext(ctx)
		require.NoError(t, err)
		require.True(t, sent)
		publish(data) // повтор после сбоя commit outbox не меняет сумму
		require.Eventually(t, func() bool { return balance(18000) }, 20*time.Second, 50*time.Millisecond)
		stop()
		stop = nil
	})

	t.Run("rebuild repeats same balances", func(t *testing.T) {
		require.NoError(t, bc.Reset(ctx, bpool, client, topic, group, false))
		items, err := repo.Balances(ctx, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, items)
		stop = start()
		require.Eventually(t, func() bool { return balance(18000) }, 20*time.Second, 50*time.Millisecond)
		// Нельзя менять offsets живой группы.
		require.ErrorContains(t, bc.Reset(ctx, bpool, client, topic, group, false), "активна")
	})

	t.Run("poison DLQ and next valid record", func(t *testing.T) {
		publish([]byte(`{"broken":`))
		event.ID = uuid.New()
		event.Payload.ReceptionID = uuid.New()
		next, err := json.Marshal(event)
		require.NoError(t, err)
		publish(next)
		require.Eventually(t, func() bool { return balance(36000) }, 20*time.Second, 50*time.Millisecond)
		reader, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(topic+".dlq"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		require.NoError(t, err)
		defer reader.Close()
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		records := reader.PollRecords(readCtx, 1).Records()
		require.Len(t, records, 1)
		assert.Equal(t, []byte(`{"broken":`), records[0].Value)
		headers := map[string]string{}
		for _, h := range records[0].Headers {
			headers[h.Key] = string(h.Value)
		}
		for _, key := range []string{"error", "source_topic", "source_partition", "source_offset", "failed_at"} {
			assert.NotEmpty(t, headers[key])
		}
	})

	t.Run("database unavailable never DLQ", func(t *testing.T) {
		_, err := pool.Exec(ctx, `ALTER DATABASE billing_test ALLOW_CONNECTIONS false`)
		require.NoError(t, err)
		defer func() {
			_, err := pool.Exec(ctx, `ALTER DATABASE billing_test ALLOW_CONNECTIONS true`)
			require.NoError(t, err)
		}()
		_, err = pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='billing_test'`)
		require.NoError(t, err)
		event.ID = uuid.New()
		event.Payload.ReceptionID = uuid.New()
		next, err := json.Marshal(event)
		require.NoError(t, err)
		publish(next)
		// Дольше трёх попыток: транзиентная ошибка не превращается в постоянную.
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
		}
		offsets, err := bc.Offsets(ctx, client, topic+".dlq", true)
		require.NoError(t, err)
		var total int64
		for _, o := range offsets {
			total += o.Offset
		}
		assert.EqualValues(t, 1, total)
		_, err = pool.Exec(ctx, `ALTER DATABASE billing_test ALLOW_CONNECTIONS true`)
		require.NoError(t, err)
		require.Eventually(t, func() bool { return balance(54000) }, 20*time.Second, 50*time.Millisecond)
	})

	t.Run("read API validation and health", func(t *testing.T) {
		router := bhttp.NewRouter(log, repo, bpool, svc.Rates(), m)
		for _, tc := range []struct {
			path   string
			status int
		}{
			{"/billing/health", 200}, {"/billing/rates", 200}, {"/billing/payouts", 200}, {"/billing/payouts?page=10000", 200},
			{"/billing/payouts/" + id.String(), 200}, {"/billing/payouts?page=0", 400}, {"/billing/payouts?limit=31", 400},
			{"/billing/payouts/" + id.String() + "?page=-1", 400}, {"/billing/payouts?limit=bad", 400}, {"/billing/payouts/no-uuid", 400},
		} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			assert.Equal(t, tc.status, w.Code, tc.path)
			assert.True(t, json.Valid(w.Body.Bytes()))
		}
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		assert.Contains(t, w.Body.String(), "billing_accrued_kopecks_total")
		assert.Contains(t, w.Body.String(), "billing_duplicates_total")
	})

	t.Run("nightly reset skips yesterday", func(t *testing.T) {
		stop()
		stop = nil
		require.NoError(t, bc.Reset(ctx, bpool, client, topic, group, true))
		stop = start()
		event.ID = uuid.New()
		event.Payload.ReceptionID = uuid.New()
		next, err := json.Marshal(event)
		require.NoError(t, err)
		publish(next)
		require.Eventually(t, func() bool { return balance(18000) }, 20*time.Second, 50*time.Millisecond)
		stop()
		stop = nil
		// Имитируем сбой после DB commit до Kafka commit: намерение пережило процесс.
		offsets, err := bc.Offsets(ctx, client, topic, false)
		require.NoError(t, err)
		marker, err := json.Marshal(offsets)
		require.NoError(t, err)
		_, err = bpool.Exec(ctx, `TRUNCATE payouts,balances`)
		require.NoError(t, err)
		_, err = bpool.Exec(ctx, `INSERT INTO pending_reset (offsets) VALUES ($1)`, marker)
		require.NoError(t, err)
		require.NoError(t, bc.FinishReset(ctx, bpool, client, topic, group))
		stop = start()
		require.Eventually(t, func() bool { return balance(72000) }, 20*time.Second, 50*time.Millisecond)
	})
}
