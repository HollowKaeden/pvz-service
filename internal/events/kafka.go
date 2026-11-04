package events

import (
	"context"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Kafka публикует события через franz-go. Используется одним relay;
// соединение с брокером устанавливается лениво, вне запуска HTTP API.
type Kafka struct {
	client *kgo.Client
	topic  string
	ready  bool
}

// NewKafka не требует доступного брокера. Подтверждение all ISR и
// идемпотентный producer включены; дубли после сбоя коммита outbox всё равно
// возможны, поэтому подписчик обязан дедуплицировать по UUID события.
func NewKafka(brokers []string, topic string) (*Kafka, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...), kgo.ClientID("pvz-outbox"),
		kgo.DefaultProduceTopic(topic), kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.MaxBufferedRecords(1), kgo.MaxBufferedBytes(1<<20),
		// Relay не держит транзакцию БД бесконечно при обрыве соединения.
		// Неопределённый результат отправки безопасен: тот же event_id
		// отправится снова до перехода к следующей записи.
		kgo.AllowIdempotentProduceCancellation(),
		kgo.DialTimeout(2*time.Second), kgo.RequestTimeoutOverhead(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("клиент Kafka: %w", err)
	}
	return &Kafka{client: client, topic: topic}, nil
}

// Close освобождает соединения после остановки relay.
func (k *Kafka) Close() { k.client.Close() }

// Publish ждёт подтверждения брокера. Создание топика тоже выполняется
// фоновым relay, поэтому выключенная Kafka не препятствует запуску API.
func (k *Kafka) Publish(ctx context.Context, m Message) error {
	if !k.ready {
		if err := k.ensureTopic(ctx); err != nil {
			return err
		}
		k.ready = true
	}
	err := k.client.ProduceSync(ctx, &kgo.Record{Key: m.Key, Value: m.Value}).FirstErr()
	if err != nil {
		k.ready = false
		return fmt.Errorf("отправка в Kafka: %w", err)
	}
	return nil
}

func (k *Kafka) ensureTopic(ctx context.Context) error {
	request := kmsg.NewPtrCreateTopicsRequest()
	request.TimeoutMillis = 3000
	request.Topics = []kmsg.CreateTopicsRequestTopic{{
		Topic: k.topic, NumPartitions: 3, ReplicationFactor: 1,
		Configs: []kmsg.CreateTopicsRequestTopicConfig{
			{Name: "cleanup.policy", Value: stringPtr("delete")},
			{Name: "retention.ms", Value: stringPtr("86400000")},
			{Name: "retention.bytes", Value: stringPtr("33554432")},
			{Name: "segment.bytes", Value: stringPtr("8388608")},
			{Name: "segment.ms", Value: stringPtr("3600000")},
		},
	}}
	response, err := request.RequestWith(ctx, k.client)
	if err != nil {
		return fmt.Errorf("создание топика: %w", err)
	}
	for _, topic := range response.Topics {
		if topic.ErrorCode != 0 && topic.ErrorCode != kerr.TopicAlreadyExists.Code {
			return fmt.Errorf("топик %s: %w", topic.Topic, kerr.ErrorForCode(topic.ErrorCode))
		}
	}
	return nil
}

func stringPtr(s string) *string { return &s }
