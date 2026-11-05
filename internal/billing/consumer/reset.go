package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Offset — зафиксированная граница reset для одной партиции.
type Offset struct {
	Partition int32
	Offset    int64
}

// Offsets получает начало или конец всех партиций топика через лидеров.
func Offsets(ctx context.Context, client *kgo.Client, topic string, end bool) ([]Offset, error) {
	meta := kmsg.NewPtrMetadataRequest()
	meta.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
	m, err := meta.RequestWith(ctx, client)
	if err != nil {
		return nil, err
	}
	req := kmsg.NewPtrListOffsetsRequest()
	if len(m.Topics) != 1 {
		return nil, errors.New("брокер не вернул метаданные топика")
	}
	t := m.Topics[0]
	if t.ErrorCode != 0 {
		return nil, kerr.ErrorForCode(t.ErrorCode)
	}
	partitions := make([]kmsg.ListOffsetsRequestTopicPartition, 0, len(t.Partitions))
	for _, p := range t.Partitions {
		v := kmsg.NewListOffsetsRequestTopicPartition()
		v.Partition = p.Partition
		v.Timestamp = -2
		if end {
			v.Timestamp = -1
		}
		partitions = append(partitions, v)
	}
	req.Topics = []kmsg.ListOffsetsRequestTopic{{Topic: topic, Partitions: partitions}}
	// RequestSharded маршрутизирует каждую партицию к её лидеру.
	var offsets []Offset
	for _, shard := range client.RequestSharded(ctx, req) {
		if shard.Err != nil {
			return nil, shard.Err
		}
		resp, ok := shard.Resp.(*kmsg.ListOffsetsResponse)
		if !ok {
			return nil, errors.New("неожиданный ответ offsets")
		}
		for _, t := range resp.Topics {
			for _, p := range t.Partitions {
				if p.ErrorCode != 0 {
					return nil, kerr.ErrorForCode(p.ErrorCode)
				}
				offsets = append(offsets, Offset{Partition: p.Partition, Offset: p.Offset})
			}
		}
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i].Partition < offsets[j].Partition })
	if len(offsets) != len(partitions) {
		return nil, errors.New("неполный ответ offsets")
	}
	return offsets, nil
}

// Reset вызывается только под эксклюзивной блокировкой БД и при остановленном
// consumer. Сначала сохраняет намерение и очищает проекцию одной транзакцией.
// Затем коммитит offsets. Сбой между системами безопасно завершается FinishReset.
func Reset(ctx context.Context, pool *pgxpool.Pool, client *kgo.Client, topic, group string, end bool) error {
	if err := FinishReset(ctx, pool, client, topic, group); err != nil {
		return err
	}
	if err := ensureEmptyGroup(ctx, client, group); err != nil {
		return err
	}
	if err := EnsureTopics(ctx, client, topic); err != nil {
		return err
	}
	offsets, err := Offsets(ctx, client, topic, end)
	if err != nil {
		return err
	}
	data, err := json.Marshal(offsets)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `TRUNCATE payouts,balances`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pending_reset (offsets) VALUES ($1)`, data); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return FinishReset(ctx, pool, client, topic, group)
}

// FinishReset восстанавливает незавершённую операцию перед вступлением в группу.
// При обычном старте Kafka не нужна: без pending_reset функция делает лишь SELECT.
func FinishReset(ctx context.Context, pool *pgxpool.Pool, client *kgo.Client, topic, group string) error {
	var data []byte
	err := pool.QueryRow(ctx, `SELECT offsets FROM pending_reset WHERE singleton`).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := ensureEmptyGroup(ctx, client, group); err != nil {
		return err
	}
	var offsets []Offset
	if err := json.Unmarshal(data, &offsets); err != nil {
		return err
	}
	req := kmsg.NewPtrOffsetCommitRequest()
	req.Group = group
	partitions := make([]kmsg.OffsetCommitRequestTopicPartition, 0, len(offsets))
	for _, p := range offsets {
		v := kmsg.NewOffsetCommitRequestTopicPartition()
		v.Partition = p.Partition
		v.Offset = p.Offset
		partitions = append(partitions, v)
	}
	req.Topics = []kmsg.OffsetCommitRequestTopic{{Topic: topic, Partitions: partitions}}
	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		return err
	}
	for _, t := range resp.Topics {
		for _, p := range t.Partitions {
			if p.ErrorCode != 0 {
				return kerr.ErrorForCode(p.ErrorCode)
			}
		}
	}
	_, err = pool.Exec(ctx, `DELETE FROM pending_reset WHERE singleton`)
	return err
}

func ensureEmptyGroup(ctx context.Context, client *kgo.Client, group string) error {
	req := kmsg.NewPtrDescribeGroupsRequest()
	req.Groups = []string{group}
	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		return err
	}
	for _, g := range resp.Groups {
		if g.ErrorCode != 0 && g.ErrorCode != kerr.GroupIDNotFound.Code {
			return kerr.ErrorForCode(g.ErrorCode)
		}
		if len(g.Members) > 0 {
			return fmt.Errorf("группа %s активна: остановите consumer перед reset", group)
		}
	}
	return nil
}
