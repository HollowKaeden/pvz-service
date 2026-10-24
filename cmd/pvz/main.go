// Команда pvz — сервис приёмки товаров на ПВЗ.
//
//	pvz [serve]   HTTP API на :8080 (подкоманда по умолчанию)
//	pvz migrate   применить миграции БД и выйти
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
	"golang.org/x/sync/errgroup"

	"github.com/HollowKaeden/pvz-service/internal/auth"
	"github.com/HollowKaeden/pvz-service/internal/config"
	"github.com/HollowKaeden/pvz-service/internal/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/service"
	httptransport "github.com/HollowKaeden/pvz-service/internal/transport/http"
)

const (
	// shutdownTimeout — сколько ждать начатые запросы при остановке. docker stop
	// присылает SIGKILL через 10 с, поэтому остановка должна уложиться раньше.
	shutdownTimeout = 8 * time.Second
	// tokenTTL — срок жизни выданного токена.
	tokenTTL = 24 * time.Hour
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
		"serve":   serve,
		"migrate": migrate,
	}
	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("неизвестная подкоманда %q (есть: serve, migrate)", name)
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

	db := postgres.NewDB(pool)
	txManager, err := manager.New(trmpgx.NewDefaultFactory(pool))
	if err != nil {
		return fmt.Errorf("менеджер транзакций: %w", err)
	}
	pvzRepo := postgres.NewPVZRepository(db)
	pvzService, err := service.NewPVZ(ctx, pvzRepo, postgres.NewCityRepository(db))
	if err != nil {
		return err
	}
	tokens := auth.NewTokens(cfg.JWTSecret, tokenTTL)
	router := httptransport.NewRouter(log, httptransport.Deps{
		DB:         pool,
		Tokens:     tokens,
		Auth:       service.NewAuth(tokens),
		PVZ:        pvzService,
		Receptions: service.NewReception(txManager, pvzRepo, postgres.NewReceptionRepository(db)),
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

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return runHTTP(ctx, httpSrv, httpLn, log.With(slog.String("server", "http")))
	})

	err = g.Wait()
	log.InfoContext(ctx, "pvz остановлен")
	return err
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
