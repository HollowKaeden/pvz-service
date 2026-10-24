// Package config читает настройки сервиса из переменных окружения.
//
// Без переменных сервис работает с локальными значениями по умолчанию; всё, что
// отличается в проде, задаётся через env. Исключение — DATABASE_URL: строк
// подключения в коде нет, поэтому её нужно задать всегда (см. .env.example).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// Config — настройки pvz.
type Config struct {
	// HTTPAddr — адрес HTTP API (HTTP_ADDR), по условию задания порт 8080.
	HTTPAddr string
	// LogLevel — минимальный уровень логов (LOG_LEVEL: debug, info, warn, error).
	LogLevel slog.Level

	// DatabaseURL — строка подключения к Postgres (DATABASE_URL), обязательная.
	DatabaseURL string
	// DBMaxConns — размер пула соединений (DB_MAX_CONNS). В проде маленький:
	// Postgres общий на все проекты сервера.
	DBMaxConns int32
	// MigrateOnStart — применять миграции при запуске serve (MIGRATE_ON_START).
	MigrateOnStart bool
}

// Load собирает Config из окружения и возвращает все ошибки разом, чтобы
// неправильный конфиг чинился за один перезапуск.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    envOr("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	var errs []error
	if err := cfg.LogLevel.UnmarshalText([]byte(envOr("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	if cfg.HTTPAddr == "" {
		errs = append(errs, errors.New("HTTP_ADDR: пустое значение"))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: не задана"))
	}

	// pgxpool хранит размер пула в int32, поэтому разбираем сразу в 32 бита.
	maxConns, err := strconv.ParseInt(envOr("DB_MAX_CONNS", "10"), 10, 32)
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("DB_MAX_CONNS: %w", err))
	case maxConns < 1:
		errs = append(errs, fmt.Errorf("DB_MAX_CONNS: нужно число от 1, получено %d", maxConns))
	default:
		cfg.DBMaxConns = int32(maxConns)
	}

	cfg.MigrateOnStart, err = strconv.ParseBool(envOr("MIGRATE_ON_START", "false"))
	if err != nil {
		errs = append(errs, fmt.Errorf("MIGRATE_ON_START: %w", err))
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
