// Package config читает настройки сервиса из переменных окружения.
//
// Без переменных сервис работает с локальными значениями по умолчанию; всё, что
// отличается в проде, задаётся через env.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Config — настройки pvz.
type Config struct {
	// HTTPAddr — адрес HTTP API (HTTP_ADDR), по условию задания порт 8080.
	HTTPAddr string
	// LogLevel — минимальный уровень логов (LOG_LEVEL: debug, info, warn, error).
	LogLevel slog.Level
}

// Load собирает Config из окружения и возвращает все ошибки разом, чтобы
// неправильный конфиг чинился за один перезапуск.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr: envOr("HTTP_ADDR", ":8080"),
	}

	var errs []error
	if err := cfg.LogLevel.UnmarshalText([]byte(envOr("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	if cfg.HTTPAddr == "" {
		errs = append(errs, errors.New("HTTP_ADDR: пустое значение"))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("конфиг: %w", err)
	}
	return cfg, nil
}

// envOr возвращает значение переменной или def, если переменная не задана.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
