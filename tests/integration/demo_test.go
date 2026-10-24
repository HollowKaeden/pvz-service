//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/demo"
	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// demo-reset: данные посетителей исчезают, остаётся эталонный набор.
// Повторный сброс даёт тот же набор.
func TestDemoReset(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	moderator, employee := login(t, domain.RoleModerator), login(t, domain.RoleEmployee)
	visitorPVZ := createPVZ(t, moderator, "Казань")

	for range 2 {
		require.NoError(t, postgres.ClearData(ctx, pool))
		require.NoError(t, demo.Seed(ctx, services))

		list := listPVZ(t, employee, "")
		require.Len(t, list, 3)
		byCity := map[oapi.PVZCity]oapi.PVZWithReceptions{}
		for _, item := range list {
			assert.NotEqual(t, visitorPVZ, *item.PVZ.ID, "ПВЗ посетителя удалён")
			byCity[item.PVZ.City] = item
		}

		// Москва: новая открытая приёмка с 2 товарами сверху, под ней закрытая с 5.
		moscow := *byCity["Москва"].Receptions
		require.Len(t, moscow, 2)
		assert.Equal(t, oapi.ReceptionStatusInProgress, moscow[0].Reception.Status)
		assert.Len(t, *moscow[0].Products, 2)
		assert.Equal(t, oapi.ReceptionStatusClose, moscow[1].Reception.Status)
		assert.Len(t, *moscow[1].Products, 5)

		spb := *byCity["Санкт-Петербург"].Receptions
		require.Len(t, spb, 1)
		assert.Equal(t, oapi.ReceptionStatusClose, spb[0].Reception.Status)
		assert.Len(t, *spb[0].Products, 3)

		assert.Empty(t, *byCity["Казань"].Receptions)
	}

	// С эталоном можно сразу работать: на Казани открыть приёмку, в Москве
	// закрыть открытую.
	var kazan, moscow uuid.UUID
	for _, item := range listPVZ(t, employee, "") {
		switch item.PVZ.City {
		case "Казань":
			kazan = *item.PVZ.ID
		case "Москва":
			moscow = *item.PVZ.ID
		}
	}
	mustCall(t, http.StatusCreated, http.MethodPost, "/receptions", employee, map[string]any{"pvzId": kazan}, nil)
	mustCall(t, http.StatusOK, http.MethodPost, "/pvz/"+moscow.String()+"/close_last_reception", employee, nil, nil)
}
