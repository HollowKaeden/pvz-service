.PHONY: help up down build run migrate generate lint fmt test hooks

# Локальные настройки (cp .env.example .env) уходят в окружение команд.
# Значения из .env важнее переменных, заданных в shell.
-include .env
export

help: ## список команд
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-10s %s\n", $$1, $$2}'

up: ## поднять локальный Postgres (compose.yaml)
	docker compose up -d --wait

down: ## остановить локальный Postgres (данные остаются в volume)
	docker compose down

build: ## собрать бинарники в bin/
	go build -o bin/ ./cmd/...

run: ## запустить pvz локально (настройки из .env, см. README)
	go run ./cmd/pvz

migrate: ## применить миграции БД
	go run ./cmd/pvz migrate

generate: ## кодогенерация
	go generate ./...

lint: ## golangci-lint (формат и линтеры)
	golangci-lint run

fmt: ## отформатировать код (gofmt + goimports)
	golangci-lint fmt

test: ## unit-тесты с детектором гонок
	go test -race ./...

hooks: ## включить pre-commit hook из .githooks/
	git config core.hooksPath .githooks
