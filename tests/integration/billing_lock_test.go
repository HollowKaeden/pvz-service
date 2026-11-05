//go:build integration

package integration

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	bp "github.com/HollowKaeden/pvz-service/internal/billing/repository/postgres"
)

// delayedDisconnectConn задерживает только конец сессии: PostgreSQL ещё
// не получил Terminate/TCP close, хотя pgx уже считает соединение закрытым.
// Обычные SQL и ответы сервера передаются без изменений. Реальные сокеты
// закрываются явно при cleanup теста, поэтому задержка не зависит от sleep.
type delayedDisconnectConn struct{ net.Conn }

func (c delayedDisconnectConn) Write(p []byte) (int, error) {
	if bytes.Equal(p, []byte{'X', 0, 0, 0, 4}) {
		return len(p), nil
	} // PostgreSQL Terminate
	return c.Conn.Write(p)
}

func (delayedDisconnectConn) Close() error { return nil }

// Проверяется передача session-lock между разными соединениями. Тот же
// пул мог бы повторно выдать соединение владельца: advisory-lock реентерабелен,
// и такое «повторное получение» не доказывает освобождение на стороне БД.
func TestBillingLockRelease(t *testing.T) {
	db := billingPool(t, "billing_lock_test")
	contender, err := bp.NewPool(t.Context(), db.Config().ConnString(), 3)
	require.NoError(t, err)
	defer contender.Close()
	cfg := db.Config()
	dial := cfg.ConnConfig.DialFunc
	var sockets []net.Conn
	var mu sync.Mutex
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		sockets = append(sockets, conn)
		mu.Unlock()
		return delayedDisconnectConn{conn}, nil
	}
	owner, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		owner.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range sockets {
			_ = conn.Close()
		}
	})
	for i := range 50 {
		func() {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			unlock, err := bp.Lock(ctx, owner)
			require.NoError(t, err, "iteration %d", i)
			defer func() {
				if unlock != nil {
					unlock()
				}
			}()
			unexpected, err := bp.Lock(t.Context(), contender)
			if err == nil {
				unexpected()
				t.Fatal("другой процесс получил удерживаемую блокировку")
			}
			require.ErrorContains(t, err, "остановите consumer")
			if i%2 == 0 {
				cancel()
			} // в serve контекст уже отменён при shutdown
			unlock()
			unlock = nil
			// Без Eventually и sleep: возврат из unlock означает, что блокировка
			// уже свободна, а не только что клиент закрыл свой сокет.
			next, err := bp.Lock(t.Context(), contender)
			require.NoError(t, err, "iteration %d", i)
			next()
		}()
	}
}
