// Команда pvz — сервис приёмки товаров на ПВЗ.
//
//	pvz [serve]   HTTP API на :8080 (подкоманда по умолчанию)
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

	"golang.org/x/sync/errgroup"

	"github.com/HollowKaeden/pvz-service/internal/config"
	httptransport "github.com/HollowKaeden/pvz-service/internal/transport/http"
)

// shutdownTimeout — сколько ждать начатые запросы при остановке. docker stop
// присылает SIGKILL через 10 с, поэтому остановка должна уложиться раньше.
const shutdownTimeout = 8 * time.Second

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
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if len(args) > 0 {
		return fmt.Errorf("лишние аргументы: %q", args)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level.Set(cfg.LogLevel)

	// SIGTERM приходит от docker stop, SIGINT — от Ctrl+C при локальном запуске.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		return serve(ctx, cfg, log)
	default:
		return fmt.Errorf("неизвестная подкоманда %q (есть: serve)", cmd)
	}
}

// serve запускает все серверы процесса в одном errgroup: сигнал или падение
// любого из них отменяет общий контекст, и остальные плавно останавливаются.
func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	httpSrv := &http.Server{
		Handler:           httptransport.NewRouter(log),
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
