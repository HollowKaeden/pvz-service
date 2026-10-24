package config

import (
	"log/slog"
	"net/netip"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	const (
		dbURL  = "postgres://pvz:pvz@localhost:5432/pvz"
		secret = "0123456789abcdef0123456789abcdef"
	)

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr []string // подстроки, которые должны быть в ошибке
	}{
		{
			name: "значения по умолчанию",
			env:  map[string]string{"DATABASE_URL": dbURL, "JWT_SECRET": secret},
			want: Config{
				HTTPAddr:    ":8080",
				LogLevel:    slog.LevelInfo,
				DatabaseURL: dbURL,
				DBMaxConns:  10,
				JWTSecret:   []byte(secret),
			},
		},
		{
			name: "из окружения",
			env: map[string]string{
				"HTTP_ADDR":        "127.0.0.1:9090",
				"LOG_LEVEL":        "DEBUG",
				"DATABASE_URL":     dbURL,
				"DB_MAX_CONNS":     "5",
				"MIGRATE_ON_START": "true",
				"JWT_SECRET":       secret,
				"TRUSTED_PROXIES":  "172.30.0.2, 10.1.2.3/8,::1",
			},
			want: Config{
				HTTPAddr:       "127.0.0.1:9090",
				LogLevel:       slog.LevelDebug,
				DatabaseURL:    dbURL,
				DBMaxConns:     5,
				MigrateOnStart: true,
				JWTSecret:      []byte(secret),
				TrustedProxies: []netip.Prefix{
					netip.MustParsePrefix("172.30.0.2/32"),
					netip.MustParsePrefix("10.0.0.0/8"),
					netip.MustParsePrefix("::1/128"),
				},
			},
		},
		{
			name: "все ошибки разом",
			env: map[string]string{
				"HTTP_ADDR":        "",
				"LOG_LEVEL":        "verbose",
				"DB_MAX_CONNS":     "много",
				"MIGRATE_ON_START": "наверное",
				"TRUSTED_PROXIES":  "caddy",
			},
			wantErr: []string{
				"HTTP_ADDR", "LOG_LEVEL", "DATABASE_URL", "DB_MAX_CONNS", "MIGRATE_ON_START", "JWT_SECRET", "TRUSTED_PROXIES",
			},
		},
		{
			name:    "пул не может быть пустым",
			env:     map[string]string{"DATABASE_URL": dbURL, "JWT_SECRET": secret, "DB_MAX_CONNS": "0"},
			wantErr: []string{"DB_MAX_CONNS"},
		},
		{
			name:    "короткий секрет",
			env:     map[string]string{"DATABASE_URL": dbURL, "JWT_SECRET": "secret"},
			wantErr: []string{"JWT_SECRET"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{
				"HTTP_ADDR", "LOG_LEVEL", "DATABASE_URL", "DB_MAX_CONNS", "MIGRATE_ON_START", "JWT_SECRET", "TRUSTED_PROXIES",
			} {
				unsetenv(t, key)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := Load()

			if tt.wantErr != nil {
				require.Error(t, err)
				for _, sub := range tt.wantErr {
					assert.ErrorContains(t, err, sub)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// unsetenv убирает переменную на время теста: t.Setenv умеет только задавать,
// а переменная из окружения разработчика не должна влиять на результат.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // запоминает старое значение и вернёт его после теста
	require.NoError(t, os.Unsetenv(key))
}
