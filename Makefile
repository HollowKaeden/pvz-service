.PHONY: help up down build run run-billing billing-rebuild migrate generate lint fmt test test-int cover load load-up load-down load-linux hooks

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

up: ## поднять локальные Postgres, Kafka и Prometheus (compose.yaml)
	docker compose up -d --wait
	docker compose exec -T postgres sh /docker-entrypoint-initdb.d/billing.sh

down: ## остановить локальные зависимости (данные остаются в volume)
	docker compose -f compose.yaml -f compose.load.yaml down

build: ## собрать бинарники в bin/
	go build -o bin/ ./cmd/...

run: ## запустить pvz локально (настройки из .env, см. README)
	go run ./cmd/pvz

run-billing: ## запустить billing (во втором терминале)
	go run ./cmd/billing

billing-rebuild: ## очистить проекцию и перемотать Kafka (сначала остановить billing)
	go run ./cmd/billing rebuild

migrate: ## применить миграции БД
	go run ./cmd/pvz migrate
	go run ./cmd/billing migrate

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

# Соседний репозиторий используется как CLI, не как Go-зависимость/подмодуль.
LOADGEN_DIR ?= ../http-loadgen
LOAD_URL ?= http://127.0.0.1:8080
LOAD_BILLING_URL ?= http://127.0.0.1:8081
LOAD_ARGS ?=
PVZ_LOAD_IMAGE ?= ghcr.io/hollowkaeden/pvz-service:latest
LOAD_NETWORK ?= $(if $(COMPOSE_PROJECT_NAME),$(COMPOSE_PROJECT_NAME),pvz-service)_default
ifeq ($(OS),Windows_NT)
LOADGEN_EXE := .exe
endif

load: ## нагрузка своим http-loadgen: 1000 RPS, JSON в tmp/load (сервисы запущены)
	go -C "$(LOADGEN_DIR)" build -o "$(abspath bin/loadgen$(LOADGEN_EXE))" ./cmd/loadgen
	go run ./cmd/pvz-load -loadgen-bin "$(abspath bin/loadgen$(LOADGEN_EXE))" -url "$(LOAD_URL)" -billing-url "$(LOAD_BILLING_URL)" $(LOAD_ARGS)

load-up: up ## добавить Linux pvz+billing из CI-образа к локальным зависимостям
	docker compose -f compose.yaml -f compose.load.yaml up -d --wait pvz billing caddy prometheus

load-down: ## остановить только pvz+billing нагрузочного окружения (данные остаются)
	docker compose -f compose.yaml -f compose.load.yaml stop pvz billing
	docker compose up -d --wait caddy prometheus

load-linux: ## тот же тест Linux-клиентом внутри Docker (рекомендуется на Windows)
	mkdir -p tmp/load-linux
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tmp/load-linux/pvz-load ./cmd/pvz-load
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C "$(LOADGEN_DIR)" build -o "$(abspath tmp/load-linux/loadgen)" ./cmd/loadgen
	docker run --rm --network "$(LOAD_NETWORK)" --mount "type=bind,source=$(abspath tmp/load-linux),target=/bench" --entrypoint /bench/pvz-load "$(PVZ_LOAD_IMAGE)" -url http://pvz:8080 -billing-url http://billing:8081 -loadgen-bin /bench/loadgen -out /bench/results $(LOAD_ARGS)
