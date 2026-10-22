package config

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr []string // подстроки, которые должны быть в ошибке
	}{
		{
			name: "значения по умолчанию",
			want: Config{HTTPAddr: ":8080", LogLevel: slog.LevelInfo},
		},
		{
			name: "из окружения",
			env:  map[string]string{"HTTP_ADDR": "127.0.0.1:9090", "LOG_LEVEL": "DEBUG"},
			want: Config{HTTPAddr: "127.0.0.1:9090", LogLevel: slog.LevelDebug},
		},
		{
			name:    "все ошибки разом",
			env:     map[string]string{"HTTP_ADDR": "", "LOG_LEVEL": "verbose"},
			wantErr: []string{"HTTP_ADDR", "LOG_LEVEL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"HTTP_ADDR", "LOG_LEVEL"} {
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
