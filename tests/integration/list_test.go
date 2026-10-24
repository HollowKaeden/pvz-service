//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// insertReception добавляет закрытую приёмку с заданной датой и товарами
// прямо в базу: через API дату приёмки не задать, её ставит сервис.
func insertReception(t *testing.T, pvzID uuid.UUID, at time.Time, productTypes ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO receptions (id, pvz_id, date_time, status_id)
		SELECT $1, $2, $3, s.id FROM reception_statuses s WHERE s.name = 'close'`,
		id, pvzID, at)
	require.NoError(t, err)
	for i, typ := range productTypes {
		_, err := pool.Exec(ctx, `
			INSERT INTO products (id, reception_id, type_id, date_time)
			SELECT $1, $2, t.id, $3 FROM product_types t WHERE t.name = $4`,
			uuid.New(), id, at.Add(time.Duration(i+1)*time.Minute), typ)
		require.NoError(t, err)
	}
	return id
}

// summary — ответ GET /pvz в виде, удобном для сравнения: id ПВЗ и id их
// приёмок в порядке ответа.
type summary struct {
	pvz        uuid.UUID
	receptions []uuid.UUID
}

func summarize(list []oapi.PVZWithReceptions) []summary {
	out := make([]summary, len(list))
	for i, item := range list {
		out[i] = summary{pvz: *item.PVZ.ID, receptions: []uuid.UUID{}}
		for _, r := range *item.Receptions {
			out[i].receptions = append(out[i].receptions, *r.Reception.ID)
		}
	}
	return out
}

func TestPVZList(t *testing.T) {
	resetDB(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	day := func(month time.Month, d int) time.Time { return time.Date(2026, month, d, 12, 0, 0, 0, time.UTC) }

	// ПВЗ заводятся по очереди, поэтому pvzC новее всех.
	pvzA := createPVZ(t, moderator, "Москва")
	pvzB := createPVZ(t, moderator, "Санкт-Петербург")
	pvzC := createPVZ(t, moderator, "Казань") // без приёмок
	recA1 := insertReception(t, pvzA, day(1, 10), "обувь", "одежда")
	recA2 := insertReception(t, pvzA, day(3, 10))
	recB := insertReception(t, pvzB, day(2, 10), "электроника")

	t.Run("без фильтра: все ПВЗ, новые сверху", func(t *testing.T) {
		list := listPVZ(t, employee, "")

		assert.Equal(t, []summary{
			{pvz: pvzC, receptions: []uuid.UUID{}},
			{pvz: pvzB, receptions: []uuid.UUID{recB}},
			{pvz: pvzA, receptions: []uuid.UUID{recA2, recA1}},
		}, summarize(list))

		// Товары приёмки в порядке добавления, у приёмки без товаров — [].
		products := (*list[2].Receptions)[1].Products
		require.Len(t, *products, 2)
		assert.Equal(t, oapi.ProductType("обувь"), (*products)[0].Type)
		assert.Equal(t, oapi.ProductType("одежда"), (*products)[1].Type)
		assert.Equal(t, recA1, (*products)[0].ReceptionID)
		assert.Empty(t, *(*list[2].Receptions)[0].Products)
		assert.Equal(t, oapi.PVZCity("Москва"), list[2].PVZ.City)
	})

	tests := []struct {
		name  string
		query string
		want  []summary
	}{
		{
			name:  "период: только ПВЗ с приёмками в нём и только эти приёмки",
			query: "startDate=2026-01-01T00:00:00Z&endDate=2026-01-31T23:59:59Z",
			want:  []summary{{pvz: pvzA, receptions: []uuid.UUID{recA1}}},
		},
		{
			name:  "только начало периода",
			query: "startDate=2026-02-01T00:00:00Z",
			want: []summary{
				{pvz: pvzB, receptions: []uuid.UUID{recB}},
				{pvz: pvzA, receptions: []uuid.UUID{recA2}},
			},
		},
		{
			name:  "только конец периода",
			query: "endDate=2026-02-28T00:00:00Z",
			want: []summary{
				{pvz: pvzB, receptions: []uuid.UUID{recB}},
				{pvz: pvzA, receptions: []uuid.UUID{recA1}},
			},
		},
		{
			name:  "границы входят в период",
			query: "startDate=2026-02-10T12:00:00Z&endDate=2026-02-10T12:00:00Z",
			want:  []summary{{pvz: pvzB, receptions: []uuid.UUID{recB}}},
		},
		{
			// Та же граница в другом часовом поясе.
			name:  "часовой пояс в дате",
			query: "startDate=2026-02-10T15:00:00%2B03:00&endDate=2026-02-10T15:00:00%2B03:00",
			want:  []summary{{pvz: pvzB, receptions: []uuid.UUID{recB}}},
		},
		{
			name:  "период без приёмок",
			query: "startDate=2025-01-01T00:00:00Z&endDate=2025-12-31T00:00:00Z",
			want:  []summary{},
		},
		{
			name:  "вторая страница",
			query: "page=2&limit=2",
			want:  []summary{{pvz: pvzA, receptions: []uuid.UUID{recA2, recA1}}},
		},
		{
			name:  "страница за концом списка",
			query: "page=3&limit=2",
			want:  []summary{},
		},
		{
			// Пагинация считается по уже отфильтрованным ПВЗ.
			name:  "фильтр и пагинация вместе",
			query: "startDate=2026-02-01T00:00:00Z&page=2&limit=1",
			want:  []summary{{pvz: pvzA, receptions: []uuid.UUID{recA2}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, summarize(listPVZ(t, employee, tt.query)))
		})
	}

	t.Run("модератор тоже видит список", func(t *testing.T) {
		assert.Len(t, listPVZ(t, moderator, ""), 3)
	})
	t.Run("неверные параметры", func(t *testing.T) {
		for _, query := range []string{"limit=0", "limit=31", "page=0", "startDate=2026-02-01T00:00:00Z&endDate=2026-01-01T00:00:00Z"} {
			status, body := call(t, http.MethodGet, "/pvz?"+query, employee, nil)
			assert.Equal(t, http.StatusBadRequest, status, query)
			assert.Contains(t, string(body), "неверные параметры списка", query)
		}
	})
}
