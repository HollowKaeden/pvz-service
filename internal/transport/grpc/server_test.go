package grpctransport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/transport/grpc/pvzv1"
)

// fakePVZ — PVZService для тестов: отдаёт заданный список или ошибку.
type fakePVZ struct {
	list []domain.PVZ
	err  error
}

func (f fakePVZ) All(context.Context) ([]domain.PVZ, error) { return f.list, f.err }

// dial поднимает сервер в памяти (bufconn, без сети) и возвращает
// соединение с ним. Лог сервера пишется в logs.
func dial(t *testing.T, pvz PVZService, logs *bytes.Buffer) *grpc.ClientConn {
	t.Helper()
	ln := bufconn.Listen(1 << 20)
	srv := NewServer(slog.New(slog.NewJSONHandler(logs, nil)), pvz)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGetPVZList(t *testing.T) {
	registered := time.Date(2026, 10, 24, 12, 4, 5, 123456000, time.UTC)
	list := []domain.PVZ{
		{ID: uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000002"), RegistrationDate: registered, City: "Москва"},
		{ID: uuid.MustParse("0192f1a4-7b3c-7d2e-8f00-000000000001"), RegistrationDate: registered.Add(-time.Hour), City: "Казань"},
	}

	t.Run("все ПВЗ", func(t *testing.T) {
		var logs bytes.Buffer
		client := pvzv1.NewPVZServiceClient(dial(t, fakePVZ{list: list}, &logs))

		resp, err := client.GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})

		require.NoError(t, err)
		require.Len(t, resp.GetPvzs(), 2)
		got := resp.GetPvzs()[0]
		assert.Equal(t, "0192f1a4-7b3c-7d2e-8f00-000000000002", got.GetId())
		assert.Equal(t, "Москва", got.GetCity())
		assert.Equal(t, registered, got.GetRegistrationDate().AsTime())
		assert.Equal(t, "Казань", resp.GetPvzs()[1].GetCity())
		assert.Contains(t, logs.String(), `"method":"/pvz.v1.PVZService/GetPVZList"`)
		assert.Contains(t, logs.String(), `"code":"OK"`)
	})
	t.Run("ПВЗ нет", func(t *testing.T) {
		client := pvzv1.NewPVZServiceClient(dial(t, fakePVZ{}, &bytes.Buffer{}))

		resp, err := client.GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})

		require.NoError(t, err)
		assert.Empty(t, resp.GetPvzs())
	})
	t.Run("внутренняя ошибка не уходит клиенту", func(t *testing.T) {
		var logs bytes.Buffer
		errDB := fmt.Errorf("все ПВЗ: %w", errors.New("dial tcp 10.0.0.5:5432: connection refused"))
		client := pvzv1.NewPVZServiceClient(dial(t, fakePVZ{err: errDB}, &logs))

		_, err := client.GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Equal(t, "внутренняя ошибка сервера", st.Message())
		assert.Contains(t, logs.String(), "10.0.0.5", "подробности в логе")
		assert.Contains(t, logs.String(), `"code":"Internal"`)
	})
	t.Run("клиент ушёл", func(t *testing.T) {
		var logs bytes.Buffer
		client := pvzv1.NewPVZServiceClient(dial(t, fakePVZ{err: fmt.Errorf("все ПВЗ: %w", context.Canceled)}, &logs))

		_, err := client.GetPVZList(context.Background(), &pvzv1.GetPVZListRequest{})

		assert.Equal(t, codes.Canceled, status.Code(err))
		assert.NotContains(t, logs.String(), "ошибка обработки вызова", "отмена — не ошибка сервиса")
	})
}

// Reflection включён: grpcurl узнаёт методы без proto-файла.
func TestReflection(t *testing.T) {
	client := reflectionpb.NewServerReflectionClient(dial(t, fakePVZ{}, &bytes.Buffer{}))
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	require.NoError(t, stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{},
	}))
	resp, err := stream.Recv()
	require.NoError(t, err)

	var names []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		names = append(names, s.GetName())
	}
	assert.Contains(t, names, "pvz.v1.PVZService")
}
