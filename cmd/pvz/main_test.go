package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HollowKaeden/pvz-service/internal/config"
)

// Начатый запрос дорабатывает после сигнала остановки, новые соединения
// больше не принимаются, runHTTP возвращает nil.
func TestRunHTTPGracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = io.WriteString(w, "done")
		}),
		ReadHeaderTimeout: time.Second,
	}
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	url := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- runHTTP(ctx, srv, ln, slog.New(slog.DiscardHandler)) }()

	type result struct {
		status int
		body   string
		err    error
	}
	inflight := make(chan result, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			inflight <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		inflight <- result{status: resp.StatusCode, body: string(body), err: err}
	}()
	<-started

	cancel()
	// Shutdown сначала закрывает listener: ждём, пока новые соединения начнут
	// получать отказ, и только потом отпускаем начатый запрос.
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		return false
	}, 2*time.Second, 10*time.Millisecond, "listener должен закрыться")
	close(release)

	res := <-inflight
	require.NoError(t, res.err)
	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, "done", res.body)

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(shutdownTimeout):
		t.Fatal("runHTTP не завершился")
	}
}

func TestLocalAddr(t *testing.T) {
	tests := map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:8080":      "127.0.0.1:8080",
		"127.0.0.1:9090": "127.0.0.1:9090",
		"pvz:8080":       "pvz:8080",
		"bad":            "bad",
	}
	for listen, want := range tests {
		assert.Equal(t, want, localAddr(listen), listen)
	}
}

func TestHealthcheck(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/health", r.URL.Path)
			w.WriteHeader(int(status.Load()))
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	cfg := config.Config{HTTPAddr: ln.Addr().String()}

	require.NoError(t, healthcheck(t.Context(), cfg, nil))

	status.Store(http.StatusServiceUnavailable)
	require.ErrorContains(t, healthcheck(t.Context(), cfg, nil), "ответил 503")

	require.NoError(t, srv.Close())
	assert.Error(t, healthcheck(t.Context(), cfg, nil), "сервис не запущен")
}

func TestRunUnknownCommand(t *testing.T) {
	err := run(slog.New(slog.DiscardHandler), new(slog.LevelVar), []string{"frobnicate"})
	assert.ErrorContains(t, err, "неизвестная подкоманда")
}
