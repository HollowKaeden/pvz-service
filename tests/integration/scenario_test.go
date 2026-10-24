//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

var productTypes = []string{"электроника", "одежда", "обувь"}

// Обязательный сценарий из задания: завести ПВЗ, открыть приёмку, добавить
// 50 товаров, закрыть приёмку.
func TestReceptionScenario(t *testing.T) {
	resetDB(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)

	pvzID := createPVZ(t, moderator, "Казань")

	var reception oapi.Reception
	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee,
		map[string]any{"pvzId": pvzID}, &reception)
	require.NotNil(t, reception.ID)
	assert.Equal(t, pvzID, reception.PVZID)
	assert.Equal(t, oapi.ReceptionStatusInProgress, reception.Status)

	added := make([]uuid.UUID, 50)
	for i := range added {
		var p oapi.Product
		mustCall(t, http.StatusCreated, http.MethodPost, "/products", employee,
			map[string]any{"pvzId": pvzID, "type": productTypes[i%3]}, &p)
		require.NotNil(t, p.ID)
		assert.Equal(t, *reception.ID, p.ReceptionID, "товар попал в открытую приёмку")
		added[i] = *p.ID
	}

	var closed oapi.Reception
	mustCall(t, http.StatusOK, http.MethodPost, "/pvz/"+pvzID.String()+"/close_last_reception", employee, nil, &closed)
	assert.Equal(t, *reception.ID, *closed.ID)
	assert.Equal(t, oapi.ReceptionStatusClose, closed.Status)

	// После закрытия товары не добавляются и не удаляются.
	status, _ := call(t, http.MethodPost, "/products", employee, map[string]any{"pvzId": pvzID, "type": "обувь"})
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = call(t, http.MethodPost, "/pvz/"+pvzID.String()+"/delete_last_product", employee, nil)
	assert.Equal(t, http.StatusBadRequest, status)

	// В списке ПВЗ видна закрытая приёмка со всеми 50 товарами в порядке
	// добавления.
	list := listPVZ(t, employee, "")
	require.Len(t, list, 1)
	assert.Equal(t, pvzID, *list[0].PVZ.ID)
	require.Len(t, *list[0].Receptions, 1)
	got := (*list[0].Receptions)[0]
	assert.Equal(t, oapi.ReceptionStatusClose, got.Reception.Status)
	require.Len(t, *got.Products, 50)
	for i, p := range *got.Products {
		assert.Equal(t, added[i], *p.ID)
		assert.Equal(t, oapi.ProductType(productTypes[i%3]), p.Type)
	}

	// Следующую приёмку на том же ПВЗ можно открыть.
	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee, map[string]any{"pvzId": pvzID}, nil)
}

// Удаляется товар, добавленный последним, и так до пустой приёмки.
func TestDeleteLastProductLIFO(t *testing.T) {
	resetDB(t)
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	pvzID := createPVZ(t, moderator, "Москва")
	deletePath := "/pvz/" + pvzID.String() + "/delete_last_product"

	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee, map[string]any{"pvzId": pvzID}, nil)
	status, body := call(t, http.MethodPost, deletePath, employee, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, string(body), "в приёмке нет товаров для удаления")

	for _, typ := range productTypes {
		mustCall(t, http.StatusCreated, http.MethodPost, "/products", employee,
			map[string]any{"pvzId": pvzID, "type": typ}, nil)
	}

	// Удаляем по одному: остаются первые добавленные.
	for left := len(productTypes) - 1; left >= 0; left-- {
		mustCall(t, http.StatusOK, http.MethodPost, deletePath, employee, nil, nil)
		assert.Equal(t, productTypes[:left], productTypesInDB(t, pvzID))
	}
	status, _ = call(t, http.MethodPost, deletePath, employee, nil)
	assert.Equal(t, http.StatusBadRequest, status)
}

// productTypesInDB возвращает типы товаров всех приёмок ПВЗ в порядке
// добавления, прямо из базы.
func productTypesInDB(t *testing.T, pvzID uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT t.name
		FROM products p
		JOIN product_types t ON t.id = p.type_id
		JOIN receptions r ON r.id = p.reception_id
		WHERE r.pvz_id = $1
		ORDER BY p.seq`, pvzID)
	require.NoError(t, err)
	defer rows.Close()
	types := []string{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		types = append(types, name)
	}
	require.NoError(t, rows.Err())
	return types
}
