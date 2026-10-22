.PHONY: help build run generate lint fmt test hooks

help: ## список команд
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-10s %s\n", $$1, $$2}'

build: ## собрать бинарники в bin/
	go build -o bin/ ./cmd/...

run: ## запустить pvz локально (настройки из env, см. README)
	go run ./cmd/pvz

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
