// Package config читает настройки сервиса из переменных окружения.
//
// Без переменных сервис работает с локальными значениями по умолчанию; всё, что
// отличается в проде, задаётся через env. Исключения — DATABASE_URL и
// JWT_SECRET: строк подключения и секретов в коде нет, поэтому их нужно задать
// всегда (см. .env.example).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
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

	// JWTSecret — ключ подписи токенов (JWT_SECRET), обязательный.
	JWTSecret []byte

	// TrustedProxies — адреса прокси, которым сервис верит в X-Forwarded-For
	// (TRUSTED_PROXIES: IP или подсети через запятую). В проде это Caddy.
	// Пусто по умолчанию: IP клиента берётся из самого соединения.
	TrustedProxies []netip.Prefix
}

// minJWTSecretLen — для HS256 ключ должен быть не короче самой подписи, 256 бит.
const minJWTSecretLen = 32

// Load собирает Config из окружения и возвращает все ошибки разом, чтобы
// неправильный конфиг чинился за один перезапуск.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    envOr("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   []byte(os.Getenv("JWT_SECRET")),
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
	if len(cfg.JWTSecret) < minJWTSecretLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET: нужно не меньше %d байт, сейчас %d", minJWTSecretLen, len(cfg.JWTSecret)))
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

	cfg.TrustedProxies, err = parsePrefixes(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		errs = append(errs, fmt.Errorf("TRUSTED_PROXIES: %w", err))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("конфиг: %w", err)
	}
	return cfg, nil
}

// parsePrefixes разбирает список IP и подсетей через запятую. Отдельный IP
// превращается в подсеть из одного адреса.
func parsePrefixes(s string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// envOr возвращает значение переменной или def, если переменная не задана.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
