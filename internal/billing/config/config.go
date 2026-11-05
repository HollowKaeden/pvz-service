// Package config читает настройки billing независимо от pvz (JWT не требуется).
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// Config — настройки процесса и собственной БД billing.
type Config struct {
	DatabaseURL, HTTPAddr, MetricsAddr, Topic, Group string
	Brokers                                          []string
	MaxConns                                         int32
	Migrate                                          bool
}

// Load валидирует окружение и задаёт безопасные ограничения.
func Load() (Config, error) {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	c := Config{DatabaseURL: os.Getenv("BILLING_DATABASE_URL"), HTTPAddr: get("BILLING_HTTP_ADDR", ":8081"),
		MetricsAddr: get("BILLING_METRICS_ADDR", ":9001"), Topic: get("KAFKA_TOPIC", "pvz.reception.closed"), Group: get("BILLING_CONSUMER_GROUP", "pvz-billing")}
	if c.DatabaseURL == "" {
		return c, errors.New("задайте BILLING_DATABASE_URL")
	}
	for _, b := range strings.Split(get("KAFKA_BROKERS", "localhost:9092"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.Brokers = append(c.Brokers, b)
		}
	}
	if len(c.Brokers) == 0 {
		return c, errors.New("задайте KAFKA_BROKERS")
	}
	n, err := strconv.ParseInt(get("BILLING_DB_MAX_CONNS", "3"), 10, 32)
	if err != nil || n < 3 || n > 10 {
		return c, errors.New("BILLING_DB_MAX_CONNS: целое от 3 до 10 (одно соединение занимает блокировка)")
	}
	c.MaxConns = int32(n)
	c.Migrate, err = strconv.ParseBool(get("BILLING_MIGRATE_ON_START", "true"))
	if err != nil {
		return c, errors.New("BILLING_MIGRATE_ON_START: true или false")
	}
	return c, nil
}
