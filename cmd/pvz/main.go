// Команда pvz — сервис приёмки товаров на ПВЗ.
//
//	pvz [serve]       HTTP API на :8080 и gRPC на :3000 (подкоманда по умолчанию)
//	pvz migrate       применить миграции БД и выйти
//	pvz healthcheck   проверить, что запущенный сервис отвечает (для Docker)
//	pvz demo-reset    очистить данные и создать эталонный набор демо
//
// Настройки только из переменных окружения (см. internal/config и README).
// Зависимости собираются здесь вручную, без DI-контейнеров и глобальных переменных.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	trmpgx "github.com/avito-tech/go-transaction-manager/drivers/pgxv5/v2"
	"github.com/avito-tech/go-transaction-manager/trm/v2/manager"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/HollowKaeden/pvz-service/internal/auth"
	"github.com/HollowKaeden/pvz-service/internal/config"
	"github.com/HollowKaeden/pvz-service/internal/demo"
	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/service"
	grpctransport "github.com/HollowKaeden/pvz-service/internal/transport/grpc"
	httptransport "github.com/HollowKaeden/pvz-service/internal/transport/http"
)

const (
	// shutdownTimeout — сколько ждать начатые запросы при остановке. docker stop
	// присылает SIGKILL через 10 с, поэтому остановка должна уложиться раньше.
	shutdownTimeout = 8 * time.Second
	// tokenTTL — срок жизни выданного токена.
	tokenTTL = 24 * time.Hour
	// healthcheckTimeout — сколько healthcheck ждёт ответа сервиса.
	healthcheckTimeout = 3 * time.Second
)

func main() {
	// Уровень логов известен только после чтения конфига, а ошибку конфига тоже
	// нужно записать в JSON. Поэтому логгер создаётся сразу, а уровень
	// выставляется позже через LevelVar.
	level := new(slog.LevelVar)
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).
		With(slog.String("service", "pvz"))

	if err := run(log, level, os.Args[1:]); err != nil {
		log.Error("pvz завершился с ошибкой", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(log *slog.Logger, level *slog.LevelVar, args []string) error {
	name := "serve"
	if len(args) > 0 {
		name, args = args[0], args[1:]
	}
	if len(args) > 0 {
		return fmt.Errorf("лишние аргументы: %q", args)
	}

	// Подкоманда проверяется до конфига: на опечатку в ней сервис должен
	// ответить «неизвестная подкоманда», а не жалобой на окружение.
	commands := map[string]func(context.Context, config.Config, *slog.Logger) error{
		"serve":       serve,
		"migrate":     migrate,
		"healthcheck": healthcheck,
		"demo-reset":  demoReset,
	}
	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("неизвестная подкоманда %q (есть: serve, migrate, healthcheck, demo-reset)", name)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level.Set(cfg.LogLevel)

	// SIGTERM приходит от docker stop, SIGINT — от Ctrl+C при локальном запуске.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cmd(ctx, cfg, log)
}

// migrate применяет миграции и завершается. Нужна, когда миграции не
// применяются при старте (MIGRATE_ON_START=false).
func migrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	return postgres.Migrate(ctx, pool, log)
}

// newServices собирает репозитории и сервисы pvz поверх пула соединений.
func newServices(ctx context.Context, pool *pgxpool.Pool) (demo.Services, error) {
	db := postgres.NewDB(pool)
	txManager, err := manager.New(trmpgx.NewDefaultFactory(pool))
	if err != nil {
		return demo.Services{}, fmt.Errorf("менеджер транзакций: %w", err)
	}
	pvzRepo := postgres.NewPVZRepository(db)
	receptionRepo := postgres.NewReceptionRepository(db)
	productRepo := postgres.NewProductRepository(db)
	pvzService, err := service.NewPVZ(ctx, pvzRepo, postgres.NewCityRepository(db), receptionRepo, productRepo)
	if err != nil {
		return demo.Services{}, err
	}
	productService, err := service.NewProduct(ctx, txManager, pvzRepo, receptionRepo,
		productRepo, postgres.NewProductTypeRepository(db))
	if err != nil {
		return demo.Services{}, err
	}
	return demo.Services{
		PVZ:        pvzService,
		Receptions: service.NewReception(txManager, pvzRepo, receptionRepo),
		Products:   productService,
	}, nil
}

// healthcheck запрашивает /health у сервиса, запущенного в том же контейнере, и
// завершается с ошибкой, если ответ не 200. Её вызывает healthcheck Docker: в
// образе нет ни shell, ни curl.
func healthcheck(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	url := "http://" + localAddr(cfg.HTTPAddr) + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: %s ответил %d", url, resp.StatusCode)
	}
	return nil
}

// localAddr превращает адрес, который слушает сервер, в адрес для запроса к
// нему же: ":8080" и "0.0.0.0:8080" слушают все интерфейсы, а обращаться
// нужно к конкретному.
func localAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// demoReset очищает ПВЗ, приёмки и товары и создаёт эталонный набор демо
// (internal/demo). Запускается по cron на сервере: демо публичное, и за день
// посетители заводят что угодно.
func demoReset(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc, err := newServices(ctx, pool)
	if err != nil {
		return err
	}
	if err := postgres.ClearData(ctx, pool); err != nil {
		return err
	}
	if err := demo.Seed(ctx, svc); err != nil {
		return fmt.Errorf("эталонный набор: %w", err)
	}
	log.InfoContext(ctx, "демо сброшено к эталонному набору")
	return nil
}

// serve поднимает зависимости и запускает все серверы процесса в одном
// errgroup: сигнал или падение любого из них отменяет общий контекст, и
// остальные плавно останавливаются. Пул БД закрывается последним, когда
// запросов уже нет.
func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.MigrateOnStart {
		if err := postgres.Migrate(ctx, pool, log); err != nil {
			return err
		}
	}

	svc, err := newServices(ctx, pool)
	if err != nil {
		return err
	}
	tokens := auth.NewTokens(cfg.JWTSecret, tokenTTL)
	router := httptransport.NewRouter(log, httptransport.Deps{
		TrustedProxies: cfg.TrustedProxies,
		AuthLimit:      cfg.AuthRateLimit,
		AuthWindow:     cfg.AuthRateWindow,
		DB:             pool,
		Tokens:         tokens,
		Auth:           service.NewAuth(tokens, postgres.NewUserRepository(postgres.NewDB(pool))),
		PVZ:            svc.PVZ,
		Receptions:     svc.Receptions,
		Products:       svc.Products,
	})

	httpSrv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	// Порт открывается до запуска: ошибка «порт занят» возвращается сразу,
	// а «слушает» в логе означает, что порт действительно открыт.
	httpLn, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}

	grpcSrv := grpctransport.NewServer(log.With(slog.String("server", "grpc")), svc.PVZ)
	grpcLn, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.GRPCAddr)
	if err != nil {
		_ = httpLn.Close()
		return fmt.Errorf("grpc: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return runHTTP(ctx, httpSrv, httpLn, log.With(slog.String("server", "http")))
	})
	g.Go(func() error {
		return runGRPC(ctx, grpcSrv, grpcLn, log.With(slog.String("server", "grpc")))
	})

	err = g.Wait()
	log.InfoContext(ctx, "pvz остановлен")
	return err
}

// runGRPC обслуживает srv на ln, пока не отменён ctx, затем останавливает его:
// новые вызовы не принимаются, начатые дорабатывают. Если они не успели за
// shutdownTimeout, соединения рвутся.
func runGRPC(ctx context.Context, srv *grpc.Server, ln net.Listener, log *slog.Logger) error {
	log.InfoContext(ctx, "сервер слушает", slog.String("addr", ln.Addr().String()))

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return fmt.Errorf("grpc: %w", err)
	case <-ctx.Done():
	}

	log.InfoContext(ctx, "остановка сервера")
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(shutdownTimeout):
		srv.Stop()
	}
	// После GracefulStop или Stop Serve возвращает nil.
	if err := <-errc; err != nil {
		return fmt.Errorf("grpc: %w", err)
	}
	return nil
}

// runHTTP обслуживает srv на ln, пока не отменён ctx, затем останавливает его:
// новые соединения больше не принимаются, начатые запросы дорабатывают.
func runHTTP(ctx context.Context, srv *http.Server, ln net.Listener, log *slog.Logger) error {
	log.InfoContext(ctx, "сервер слушает", slog.String("addr", ln.Addr().String()))

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		// Serve возвращает ErrServerClosed только после Shutdown, а его
		// вызываем только мы ниже, поэтому здесь всегда настоящая ошибка.
		return fmt.Errorf("http: %w", err)
	case <-ctx.Done():
	}

	log.InfoContext(ctx, "остановка сервера")
	// ctx уже отменён: для ожидания запросов нужен свой контекст с таймаутом.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http: остановка: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}
