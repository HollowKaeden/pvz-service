.PHONY: help up down build run migrate generate lint fmt test test-int cover hooks

# Локальные настройки (cp .env.example .env) уходят в окружение команд.
# Значения из .env важнее переменных, заданных в shell.
-include .env
export

# Docker Desktop на Windows: testcontainers по умолчанию пробрасывает в свой
# служебный контейнер (Ryuk) путь Windows-пайпа, и тот не стартует. Внутри
# Linux-контейнера сокет лежит здесь. MSYS_NO_PATHCONV не даёт Git Bash
# переписать этот путь в путь Windows.
ifeq ($(OS),Windows_NT)
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE = /var/run/docker.sock
export MSYS_NO_PATHCONV = 1
endif

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

generate: ## кодогенерация: HTTP-слой (oapi-codegen) и моки (mockery)
	go generate ./...
	go tool mockery

lint: ## golangci-lint (формат и линтеры)
	golangci-lint run

fmt: ## отформатировать код (gofmt + goimports)
	golangci-lint fmt

test: ## unit-тесты с детектором гонок
	go test -race ./...

test-int: ## интеграционные тесты: Postgres в Docker через testcontainers (нужен Docker)
	go test -race -tags integration -count=1 ./tests/integration/...

# Покрытие считается по коду internal/ без сгенерированных пакетов (oapi,
# pvzv1): они большие, и их покрытие ничего не говорит о качестве тестов.
comma := ,
space := $(subst ,, )
COVERPKG = $(subst $(space),$(comma),$(strip $(shell go list ./internal/... | grep -v -e /oapi -e /pvzv1)))
COVER_MIN = 75

cover: ## покрытие unit- и интеграционными тестами, не ниже COVER_MIN% (нужен Docker)
	go test -race -tags integration -count=1 -coverpkg=$(COVERPKG) -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1
	@go tool cover -func=coverage.out | tail -1 | awk '{ sub("%", "", $$3); if ($$3 + 0 < $(COVER_MIN)) { print "покрытие ниже $(COVER_MIN)%"; exit 1 } }'

hooks: ## включить pre-commit hook из .githooks/
	git config core.hooksPath .githooks
