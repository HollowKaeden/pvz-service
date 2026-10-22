package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestRunUnknownCommand(t *testing.T) {
	err := run(slog.New(slog.DiscardHandler), new(slog.LevelVar), []string{"frobnicate"})
	assert.ErrorContains(t, err, "неизвестная подкоманда")
}
