//go:build integration

// Package integration — интеграционные тесты pvz: настоящий Postgres в
// Docker (testcontainers), настоящие репозитории, сервисы, HTTP-роутер и
// gRPC-сервер.
// Запуск: make test-int (нужен Docker). Тесты идут последовательно и перед
// каждым очищают таблицы.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	trmpgx "github.com/avito-tech/go-transaction-manager/drivers/pgxv5/v2"
	"github.com/avito-tech/go-transaction-manager/trm/v2/manager"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/HollowKaeden/pvz-service/internal/auth"
	"github.com/HollowKaeden/pvz-service/internal/demo"
	"github.com/HollowKaeden/pvz-service/internal/domain"
	"github.com/HollowKaeden/pvz-service/internal/metrics"
	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/service"
	grpctransport "github.com/HollowKaeden/pvz-service/internal/transport/grpc"
	httptransport "github.com/HollowKaeden/pvz-service/internal/transport/http"
	"github.com/HollowKaeden/pvz-service/internal/transport/http/oapi"
)

// Общее окружение всех тестов пакета: поднимается один раз в TestMain.
var (
	pool     *pgxpool.Pool
	services demo.Services
	baseURL  string
	grpcConn *grpc.ClientConn  // соединение с gRPC-сервером pvz
	recorder *metrics.Recorder // метрики сервисов, HTTP и gRPC
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)

	// Та же мажорная версия Postgres, что на сервере.
	ctr, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("pvz"),
		tcpostgres.WithUsername("pvz"),
		tcpostgres.WithPassword("pvz"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintln(os.Stderr, "остановка Postgres:", err)
		}
	}()
	if err != nil {
		fmt.Fprintln(os.Stderr, "запуск Postgres:", err)
		return 1
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "строка подключения:", err)
		return 1
	}

	// Пул с запасом: в тестах на гонки два десятка запросов идут разом.
	pool, err = postgres.NewPool(ctx, url, 30)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, log); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	recorder = metrics.New()
	services, err = newServices(ctx, pool, recorder)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tokens := auth.NewTokens([]byte("integration-test-secret-0123456789abcdef"), time.Hour)
	srv := httptest.NewServer(httptransport.NewRouter(log, httptransport.Deps{
		Metrics:    recorder,
		DB:         pool,
		Tokens:     tokens,
		Auth:       service.NewAuth(tokens, postgres.NewUserRepository(postgres.NewDB(pool))),
		PVZ:        services.PVZ,
		Receptions: services.Receptions,
		Products:   services.Products,
	}))
	defer srv.Close()
	baseURL = srv.URL

	// gRPC на настоящем TCP-порту, как в проде, только порт свободный.
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "порт для gRPC:", err)
		return 1
	}
	grpcSrv := grpctransport.NewServer(log, services.PVZ, recorder)
	go func() { _ = grpcSrv.Serve(ln) }()
	defer grpcSrv.Stop()
	grpcConn, err = grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "клиент gRPC:", err)
		return 1
	}
	defer grpcConn.Close()

	return m.Run()
}

// newServices собирает сервисы так же, как cmd/pvz: настоящие репозитории и
// менеджер транзакций.
func newServices(ctx context.Context, pool *pgxpool.Pool, m service.Metrics) (demo.Services, error) {
	db := postgres.NewDB(pool)
	txManager, err := manager.New(trmpgx.NewDefaultFactory(pool))
	if err != nil {
		return demo.Services{}, err
	}
	pvzRepo := postgres.NewPVZRepository(db)
	receptionRepo := postgres.NewReceptionRepository(db)
	productRepo := postgres.NewProductRepository(db)
	pvzService, err := service.NewPVZ(ctx, pvzRepo, postgres.NewCityRepository(db), receptionRepo, productRepo, m)
	if err != nil {
		return demo.Services{}, err
	}
	productService, err := service.NewProduct(ctx, txManager, pvzRepo, receptionRepo,
		productRepo, postgres.NewProductTypeRepository(db), m)
	if err != nil {
		return demo.Services{}, err
	}
	return demo.Services{
		PVZ:        pvzService,
		Receptions: service.NewReception(txManager, pvzRepo, receptionRepo, m),
		Products:   productService,
	}, nil
}

// resetDB очищает таблицы с данными. Справочники не трогает.
func resetDB(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE products, receptions, pvz, users`)
	require.NoError(t, err)
}

// send выполняет запрос к API. body, если не nil, уходит JSON'ом. Не
// останавливает тест, поэтому годится для горутин.
func send(method, path, token string, body any) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, r)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

// call выполняет запрос к API и возвращает код и тело ответа.
func call(t *testing.T, method, path, token string, body any) (int, []byte) {
	t.Helper()
	status, data, err := send(method, path, token, body)
	require.NoError(t, err)
	return status, data
}

// mustCall выполняет запрос, проверяет код ответа и разбирает тело в out
// (если out не nil).
func mustCall(t *testing.T, wantStatus int, method, path, token string, body, out any) {
	t.Helper()
	status, data := call(t, method, path, token, body)
	require.Equal(t, wantStatus, status, "%s %s: %s", method, path, data)
	if out != nil {
		require.NoError(t, json.Unmarshal(data, out), string(data))
	}
}

// login получает токен через /dummyLogin, как настоящий клиент.
func login(t *testing.T, role domain.Role) string {
	t.Helper()
	var token string
	mustCall(t, http.StatusOK, http.MethodPost, "/dummyLogin", "", map[string]string{"role": string(role)}, &token)
	return token
}

// createPVZ заводит ПВЗ от имени модератора.
func createPVZ(t *testing.T, moderator, city string) uuid.UUID {
	t.Helper()
	var pvz oapi.PVZ
	mustCall(t, http.StatusCreated, http.MethodPost, "/pvz", moderator, map[string]string{"city": city}, &pvz)
	require.NotNil(t, pvz.ID)
	return *pvz.ID
}

// listPVZ запрашивает GET /pvz с заданной query-строкой.
func listPVZ(t *testing.T, token, query string) []oapi.PVZWithReceptions {
	t.Helper()
	var list []oapi.PVZWithReceptions
	mustCall(t, http.StatusOK, http.MethodGet, "/pvz?"+query, token, nil, &list)
	return list
}
