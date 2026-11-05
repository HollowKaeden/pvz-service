// Package consumer доставляет события в начисления с ручным коммитом offsets.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/HollowKaeden/pvz-service/internal/billing/domain"
	"github.com/HollowKaeden/pvz-service/internal/billing/metrics"
)

// Handler принимает исходный JSON события; постоянные ошибки помечены ErrPermanent.
type Handler interface {
	Apply(context.Context, []byte) error
}

// Consumer обрабатывает по одному сообщению, сохраняя порядок и ограничивая память.
type Consumer struct {
	client  *kgo.Client
	topic   string
	handler Handler
	metrics *metrics.Recorder
	log     *slog.Logger
}

// New создаёт ленивый клиент: недоступная Kafka не мешает запуску HTTP API.
func New(brokers []string, topic, group string, handler Handler, m *metrics.Recorder, log *slog.Logger) (*Consumer, error) {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumerGroup(group), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxBytes(1<<20), kgo.FetchMaxPartitionBytes(1<<20), kgo.MaxConcurrentFetches(1),
		kgo.MaxBufferedRecords(1), kgo.MaxBufferedBytes(1<<20), kgo.AllowIdempotentProduceCancellation(),
		kgo.DialTimeout(2*time.Second), kgo.RequestTimeoutOverhead(2*time.Second))
	if err != nil {
		return nil, err
	}
	return &Consumer{client: client, topic: topic, handler: handler, metrics: m, log: log}, nil
}

// Close разрешает финальную ребалансировку перед выходом из группы.
func (c *Consumer) Close() { c.client.CloseAllowingRebalance() }

// Run пережидает ошибки брокера и БД, не останавливая API.
func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := EnsureTopics(opCtx, c.client, c.topic)
		cancel()
		if err == nil {
			break
		}
		c.log.WarnContext(ctx, "Kafka пока недоступна", slog.Any("error", err))
		if !pause(ctx, time.Second) {
			return nil
		}
	}
	// Метрики продолжают обновляться и когда партиция ждёт возврата БД.
	statsCtx, stopStats := context.WithCancel(ctx)
	statsDone := make(chan struct{})
	go func() { defer close(statsDone); c.monitor(statsCtx) }()
	defer func() { stopStats(); <-statsDone }()
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, time.Second)
		fetches := c.client.PollRecords(pollCtx, 1)
		cancel()
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			for _, record := range p.Records {
				c.metrics.SetLag(p.Partition, p.HighWatermark-record.Offset)
				if c.process(ctx, record) {
					c.metrics.SetLag(p.Partition, p.HighWatermark-record.Offset-1)
				}
			}
		})
		c.client.AllowRebalance()
		if errs := fetches.Errors(); len(errs) > 0 && ctx.Err() == nil && !errors.Is(errs[0].Err, context.DeadlineExceeded) {
			c.log.WarnContext(ctx, "чтение Kafka: повтор", slog.Any("error", errs[0].Err))
			if !pause(ctx, time.Second) {
				break
			}
		}
	}
	return nil
}

func (c *Consumer) monitor(ctx context.Context) {
	for ctx.Err() == nil {
		c.updateDLQ(ctx)
		opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		start, err := Offsets(opCtx, c.client, c.topic, false)
		if err == nil {
			end, endErr := Offsets(opCtx, c.client, c.topic, true)
			if endErr == nil {
				committed := c.client.CommittedOffsets()[c.topic]
				for i, p := range end {
					position := start[i].Offset
					if offset, ok := committed[p.Partition]; ok {
						position = max(position, offset.Offset)
					}
					c.metrics.SetLag(p.Partition, p.Offset-position)
				}
			}
		}
		cancel()
		if !pause(ctx, 15*time.Second) {
			return
		}
	}
}

func (c *Consumer) process(ctx context.Context, r *kgo.Record) bool {
	delay := 250 * time.Millisecond
	permanentAttempts := 0
	for ctx.Err() == nil {
		opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.handler.Apply(opCtx, r.Value)
		cancel()
		if err == nil {
			break
		}
		if errors.Is(err, domain.ErrPermanent) {
			permanentAttempts++
			if permanentAttempts >= 3 {
				dlq := &kgo.Record{Topic: c.topic + ".dlq", Key: r.Key, Value: r.Value, Headers: []kgo.RecordHeader{
					{Key: "error", Value: []byte(err.Error())}, {Key: "source_topic", Value: []byte(r.Topic)},
					{Key: "source_partition", Value: []byte(strconv.FormatInt(int64(r.Partition), 10))},
					{Key: "source_offset", Value: []byte(strconv.FormatInt(r.Offset, 10))},
					{Key: "failed_at", Value: []byte(time.Now().UTC().Format(time.RFC3339Nano))},
				}}
				opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = c.client.ProduceSync(opCtx, dlq).FirstErr()
				cancel()
				if err == nil {
					c.metrics.Dead()
					break
				}
			}
		}
		c.log.WarnContext(ctx, "событие ждёт повторной обработки", slog.Int64("offset", r.Offset), slog.Any("error", err))
		if !pause(ctx, delay) {
			return false
		}
		delay = min(delay*2, 5*time.Second)
	}
	// Ошибка commit не должна пропустить запись: ждём подтверждения, при
	// падении процесса она придёт снова и будет отброшена по event_id.
	for ctx.Err() == nil {
		opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.client.CommitRecords(opCtx, r)
		cancel()
		if err == nil {
			return true
		}
		c.log.WarnContext(ctx, "коммит Kafka: повтор", slog.Any("error", err))
		if !pause(ctx, time.Second) {
			break
		}
	}
	return false
}

func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// EnsureTopics создаёт основной топик и DLQ с одинаково ограниченным хранением.
func EnsureTopics(ctx context.Context, client *kgo.Client, topic string) error {
	req := kmsg.NewPtrCreateTopicsRequest()
	req.TimeoutMillis = 3000
	for _, name := range []string{topic, topic + ".dlq"} {
		configs := make([]kmsg.CreateTopicsRequestTopicConfig, 0, 5)
		for key, value := range map[string]string{"retention.ms": "86400000", "retention.bytes": "33554432", "segment.bytes": "8388608", "segment.ms": "3600000", "cleanup.policy": "delete"} {
			configs = append(configs, kmsg.CreateTopicsRequestTopicConfig{Name: key, Value: &value})
		}
		req.Topics = append(req.Topics, kmsg.CreateTopicsRequestTopic{Topic: name, NumPartitions: 3, ReplicationFactor: 1, Configs: configs})
	}
	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		return err
	}
	for _, t := range resp.Topics {
		if t.ErrorCode != 0 && t.ErrorCode != kerr.TopicAlreadyExists.Code {
			return fmt.Errorf("топик %s: %w", t.Topic, kerr.ErrorForCode(t.ErrorCode))
		}
	}
	return nil
}

func (c *Consumer) updateDLQ(ctx context.Context) {
	opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	start, err := Offsets(opCtx, c.client, c.topic+".dlq", false)
	if err != nil {
		return
	}
	end, err := Offsets(opCtx, c.client, c.topic+".dlq", true)
	if err != nil {
		return
	}
	var size int64
	for i := range start {
		size += max(0, end[i].Offset-start[i].Offset)
	}
	c.metrics.DLQ.Set(float64(size))
}
