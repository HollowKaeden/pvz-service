//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/grpc/pvzv1"
)

// GetPVZList отдаёт все ПВЗ без токена, новые сверху, с теми же данными, что
// и GET /pvz.
func TestGRPCGetPVZList(t *testing.T) {
	resetDB(t)
	client := pvzv1.NewPVZServiceClient(grpcConn)

	resp, err := client.GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetPvzs(), "пустая база")

	moderator := login(t, domain.RoleModerator)
	createPVZ(t, moderator, "Казань")
	createPVZ(t, moderator, "Москва")
	// Больше, чем страница GET /pvz по умолчанию: gRPC отдаёт всё без страниц.
	for range 10 {
		createPVZ(t, moderator, "Санкт-Петербург")
	}

	resp, err = client.GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetPvzs(), 12)

	list := listPVZ(t, moderator, "limit=30")
	require.Len(t, list, 12)
	for i, item := range list {
		got := resp.GetPvzs()[i]
		assert.Equal(t, item.PVZ.ID.String(), got.GetId())
		assert.Equal(t, string(item.PVZ.City), got.GetCity())
		assert.True(t, item.PVZ.RegistrationDate.Equal(got.GetRegistrationDate().AsTime()))
	}
	assert.Equal(t, "Казань", resp.GetPvzs()[11].GetCity(), "первый заведённый — последний в списке")
}
