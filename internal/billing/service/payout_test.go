package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/billing/domain"
	"github.com/HollowKaeden/pvz-service/internal/eventschema"
)

func validEvent() eventschema.Event {
	now := time.Now().UTC()
	return eventschema.Event{ID: uuid.New(), Type: eventschema.ReceptionClosedType, Version: 1, OccurredAt: now,
		Payload: eventschema.ReceptionClosed{ReceptionID: uuid.New(), PVZID: uuid.New(), City: "Москва", OpenedAt: now.Add(-time.Minute), ClosedAt: now,
			Products: []eventschema.Product{{ID: uuid.New(), Type: "обувь"}, {ID: uuid.New(), Type: "одежда"}}}}
}

func TestInvalidEventDoesNotReachDatabase(t *testing.T) {
	s := &Payout{rates: map[string]int64{"обувь": 3000, "одежда": 5000}}
	tests := map[string]func(*eventschema.Event){
		"version": func(e *eventschema.Event) { e.Version++ }, "type": func(e *eventschema.Event) { e.Type = "other" },
		"id": func(e *eventschema.Event) { e.ID = uuid.Nil }, "pvz": func(e *eventschema.Event) { e.Payload.PVZID = uuid.Nil },
		"reception": func(e *eventschema.Event) { e.Payload.ReceptionID = uuid.Nil }, "city": func(e *eventschema.Event) { e.Payload.City = "" },
		"time":     func(e *eventschema.Event) { e.Payload.ClosedAt = e.Payload.OpenedAt.Add(-time.Second) },
		"occurred": func(e *eventschema.Event) { e.OccurredAt = e.OccurredAt.Add(time.Second) },
		"empty":    func(e *eventschema.Event) { e.Payload.Products = nil }, "unknown": func(e *eventschema.Event) { e.Payload.Products[0].Type = "еда" },
		"product":          func(e *eventschema.Event) { e.Payload.Products[0].ID = uuid.Nil },
		"repeated product": func(e *eventschema.Event) { e.Payload.Products[1].ID = e.Payload.Products[0].ID },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			change(&e)
			data, err := json.Marshal(e)
			require.NoError(t, err)
			assert.ErrorIs(t, s.Apply(t.Context(), data), domain.ErrPermanent)
		})
	}
	assert.ErrorIs(t, s.Apply(t.Context(), []byte(`{`)), domain.ErrPermanent)
	s.rates["обувь"] = math.MaxInt64
	data, err := json.Marshal(validEvent())
	require.NoError(t, err)
	assert.ErrorIs(t, s.Apply(t.Context(), data), domain.ErrPermanent)
}

func TestCommitAndDuplicateMetrics(t *testing.T) {
	data, err := json.Marshal(validEvent())
	require.NoError(t, err)
	failure := errors.New("commit failed")
	rates := []domain.Rate{{ProductType: "\u043e\u0431\u0443\u0432\u044c", Amount: 3000}, {ProductType: "\u043e\u0434\u0435\u0436\u0434\u0430", Amount: 5000}}
	for _, tc := range []struct {
		name               string
		inserted           bool
		repoErr, commitErr error
	}{
		{"success", true, nil, nil}, {"duplicate", false, nil, nil},
		{"rollback", true, nil, failure}, {"insert failure", false, failure, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newMockRepository(t)
			tx := newMockTxManager(t)
			m := newMockMetrics(t)
			r.EXPECT().Rates(mock.Anything).Return(rates, nil).Once()
			r.EXPECT().Insert(mock.Anything, mock.Anything).Return(tc.inserted, tc.repoErr).Once()
			if tc.inserted {
				r.EXPECT().Accrue(mock.Anything, mock.Anything).Return(nil).Once()
			}
			tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
				if err := fn(ctx); err != nil {
					return err
				}
				return tc.commitErr
			}).Once()
			if tc.repoErr == nil && tc.commitErr == nil {
				m.EXPECT().Applied(int64(8000), !tc.inserted).Once()
			}
			s, err := New(t.Context(), tx, r, m)
			require.NoError(t, err)
			err = s.Apply(t.Context(), data)
			if tc.repoErr != nil || tc.commitErr != nil {
				assert.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			assert.Len(t, s.Rates(), 2)
		})
	}
	r := newMockRepository(t)
	r.EXPECT().Rates(mock.Anything).Return(nil, failure).Once()
	_, err = New(t.Context(), newMockTxManager(t), r, newMockMetrics(t))
	assert.ErrorIs(t, err, failure)
}
