# pvz-service

Сервис для сотрудников пунктов выдачи заказов (ПВЗ): приёмка товаров. Решение открытого [тестового задания Авито для бэкенд-стажёров, весна 2025](https://github.com/avito-tech/tech-internship/blob/main/Tech%20Internships/Backend/Backend-trainee-assignment-spring-2025/Backend-trainee-assignment-spring-2025.md).

Учебный и портфолио-проект: в Авито решение не отправлялось.

> **Статус:** в разработке. Сейчас есть каркас сервиса: HTTP-сервер на `:8080` с `GET /health`, конфиг из env, JSON-логи, плавная остановка, CI (линтер и тесты).

## Запуск

Нужен Go 1.27+.

```bash
make run                      # или: go run ./cmd/pvz
curl localhost:8080/health    # {"status":"ok"}
```

### Настройки

Только через переменные окружения. Без них сервис запускается с локальными значениями по умолчанию.

| Переменная | По умолчанию | Что задаёт |
|---|---|---|
| `HTTP_ADDR` | `:8080` | адрес HTTP API |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Разработка

```bash
make test       # unit-тесты с -race
make lint       # golangci-lint: формат и линтеры
make fmt        # gofmt + goimports
make generate   # кодогенерация
make hooks      # один раз: pre-commit hook с линтером
```

golangci-lint ставится отдельным бинарником ([инструкция](https://golangci-lint.run/docs/welcome/install/)), версия v2.14.0, как в CI. Для `-race` нужен cgo: на Linux и macOS работает из коробки, на Windows нужен gcc (например, MinGW-w64).

CI (`.github/workflows/ci.yml`) на каждый push в `main` и PR запускает линтер, проверку `go mod tidy`, сборку и тесты.

## Контракт

- `api/openapi.yaml` — HTTP API, копия `swagger.yaml` из задания без изменений. Контракт, имена полей и значения enum'ов не меняются.
- `api/pvz.proto` — gRPC, копия из задания. `go_package` прописывается, когда в проекте появляется gRPC.
