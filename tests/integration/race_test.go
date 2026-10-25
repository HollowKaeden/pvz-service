//go:build integration

package integration

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
)

// request — один запрос для parallel.
type request struct {
	method, path, token string
	body                any
}

// parallel отправляет запросы одновременно (насколько это возможно: все ждут
// общего старта) и возвращает коды ответов в том же порядке.
func parallel(t *testing.T, reqs []request) []int {
	t.Helper()
	start := make(chan struct{})
	statuses := make([]int, len(reqs))
	errs := make([]error, len(reqs))
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Go(func() {
			<-start
			statuses[i], _, errs[i] = send(r.method, r.path, r.token, r.body)
		})
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	return statuses
}

// repeat — n одинаковых запросов.
func repeat(n int, r request) []request {
	reqs := make([]request, n)
	for i := range reqs {
		reqs[i] = r
	}
	return reqs
}

// countStatuses считает ответы по кодам.
func countStatuses(statuses []int) map[int]int {
	counts := map[int]int{}
	for _, s := range statuses {
		counts[s]++
	}
	return counts
}

// Одновременные попытки открыть приёмку на одном ПВЗ: успешна ровно одна,
// остальные видят открытую приёмку. Защищает блокировка строки ПВЗ.
//
// Уникальный индекс на открытую приёмку на время теста убирается: иначе тест
// прошёл бы и без блокировки, а проверить нужно именно её. Индекс отдельно
// проверяет TestOpenReceptionUniqueIndex.
func TestOpenReceptionRace(t *testing.T) {
	resetDB(t)
	withoutOpenReceptionIndex(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	pvzID := createPVZ(t, moderator, "Москва")

	const n = 20
	statuses := parallel(t, repeat(n, request{http.MethodPost, "/receptions", employee, map[string]any{"pvzId": pvzID}}))

	assert.Equal(t, map[int]int{http.StatusCreated: 1, http.StatusBadRequest: n - 1}, countStatuses(statuses))
	var open int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM receptions WHERE pvz_id = $1`, pvzID).Scan(&open)
	require.NoError(t, err)
	assert.Equal(t, 1, open)
}

// withoutOpenReceptionIndex удаляет уникальный индекс на открытую приёмку до
// конца теста. Определение индекса — как в migrations/pvz/00001_init.sql.
func withoutOpenReceptionIndex(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DROP INDEX receptions_one_open_per_pvz_idx`)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Если тест упал, в таблице могут остаться две открытые приёмки на
		// одном ПВЗ, и индекс не создастся. Данные теста больше не нужны.
		_, err := pool.Exec(ctx, `TRUNCATE products, receptions, pvz`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx,
			`CREATE UNIQUE INDEX receptions_one_open_per_pvz_idx ON receptions (pvz_id) WHERE status_id = 1`)
		require.NoError(t, err)
	})
}

// Одновременные удаления: каждое удаляет свой товар, ни один не удаляется
// дважды и ни один не пропускается. Защищает блокировка открытой приёмки.
func TestDeleteLastProductRace(t *testing.T) {
	resetDB(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	pvzID := createPVZ(t, moderator, "Москва")
	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee, map[string]any{"pvzId": pvzID}, nil)
	const products = 10
	for range products {
		mustCall(t, http.StatusCreated, http.MethodPost, "/products", employee,
			map[string]any{"pvzId": pvzID, "type": "обувь"}, nil)
	}

	const n = 15
	statuses := parallel(t, repeat(n, request{http.MethodPost, "/pvz/" + pvzID.String() + "/delete_last_product", employee, nil}))

	assert.Equal(t, map[int]int{http.StatusOK: products, http.StatusBadRequest: n - products}, countStatuses(statuses))
	assert.Empty(t, productTypesInDB(t, pvzID))
}

// Одновременные добавления и закрытие: каждый товар либо попал в приёмку до
// её закрытия, либо получил отказ. В закрытой приёмке ровно столько товаров,
// сколько было успешных ответов. Один товар добавлен заранее: пустую приёмку
// закрыть нельзя, а закрытие может успеть раньше всех добавлений.
func TestAddProductsWhileClosing(t *testing.T) {
	resetDB(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	pvzID := createPVZ(t, moderator, "Москва")
	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee, map[string]any{"pvzId": pvzID}, nil)
	mustCall(t, http.StatusCreated, http.MethodPost, "/products", employee,
		map[string]any{"pvzId": pvzID, "type": "обувь"}, nil)

	// Закрытие в середине пачки добавлений.
	reqs := repeat(20, request{http.MethodPost, "/products", employee, map[string]any{"pvzId": pvzID, "type": "обувь"}})
	reqs[10] = request{http.MethodPost, "/pvz/" + pvzID.String() + "/close_last_reception", employee, nil}
	statuses := parallel(t, reqs)

	assert.Equal(t, http.StatusOK, statuses[10], "закрытие прошло")
	added := 1
	for i, s := range statuses {
		if i == 10 {
			continue
		}
		assert.Contains(t, []int{http.StatusCreated, http.StatusBadRequest}, s)
		if s == http.StatusCreated {
			added++
		}
	}
	assert.Len(t, productTypesInDB(t, pvzID), added)
}
