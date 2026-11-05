// Команда billing: serve, migrate, healthcheck, demo-reset, rebuild.
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
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"

	"github.com/HollowKaeden/pvz-service/internal/billing/config"
	"github.com/HollowKaeden/pvz-service/internal/billing/consumer"
	"github.com/HollowKaeden/pvz-service/internal/billing/metrics"
	"github.com/HollowKaeden/pvz-service/internal/billing/repository/postgres"
	"github.com/HollowKaeden/pvz-service/internal/billing/service"
	httptransport "github.com/HollowKaeden/pvz-service/internal/billing/transport/http"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With(slog.String("service", "billing"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if err := run(ctx, os.Args[1:], log); err != nil {
		log.ErrorContext(ctx, "billing завершился с ошибкой", slog.Any("error", err))
		stop()
		os.Exit(1)
	}
	stop()
}

func run(ctx context.Context, args []string, log *slog.Logger) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if len(args) > 1 {
		return errors.New("лишние аргументы")
	}
	switch command {
	case "serve", "migrate", "healthcheck", "rebuild", "demo-reset":
	default:
		return fmt.Errorf("неизвестная подкоманда %q", command)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if command == "healthcheck" {
		return healthcheck(ctx, cfg.HTTPAddr)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.MaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()
	if command == "migrate" || cfg.Migrate {
		if err := postgres.Migrate(ctx, pool); err != nil {
			return err
		}
	}
	if command == "migrate" {
		return nil
	}
	unlock, err := postgres.Lock(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	admin, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.DialTimeout(2*time.Second), kgo.RequestTimeoutOverhead(2*time.Second))
	if err != nil {
		return err
	}
	defer admin.Close()
	if command == "rebuild" || command == "demo-reset" {
		opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := consumer.Reset(opCtx, pool, admin, cfg.Topic, cfg.Group, command == "demo-reset"); err != nil {
			return err
		}
		log.InfoContext(ctx, "проекция очищена, offsets установлены", slog.String("command", command))
		return nil
	}
	// Долговечное намерение reset обязано завершиться до запуска consumer.
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = consumer.FinishReset(opCtx, pool, admin, cfg.Topic, cfg.Group)
	cancel()
	if err != nil {
		return err
	}
	tx, err := manager.New(trmpgx.NewDefaultFactory(pool))
	if err != nil {
		return err
	}
	m := metrics.New()
	repo := postgres.New(pool)
	svc, err := service.New(ctx, tx, repo, m)
	if err != nil {
		return err
	}
	worker, err := consumer.New(cfg.Brokers, cfg.Topic, cfg.Group, svc, m, log)
	if err != nil {
		return err
	}
	defer worker.Close()
	api := httptransport.NewRouter(log, repo, pool, svc.Rates(), m)
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return worker.Run(ctx) })
	g.Go(func() error { return serveHTTP(ctx, cfg.HTTPAddr, api, log) })
	g.Go(func() error { return serveHTTP(ctx, cfg.MetricsAddr, mux, log) })
	return g.Wait()
}

func serveHTTP(ctx context.Context, addr string, handler http.Handler, log *slog.Logger) error {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.InfoContext(ctx, "сервер слушает", slog.String("addr", ln.Addr().String()))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthcheck(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/billing/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: HTTP %d", resp.StatusCode)
	}
	return nil
}
