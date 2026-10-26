# Образ pvz для сервера. Сборка в CI (linux/amd64), на сервере только pull.
# Миграции, контракт, Swagger UI и фронтенд (web/) встроены в бинарник
# (embed), поэтому в итоговом образе один файл.

FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY web ./web
# Статическая сборка без cgo: бинарнику не нужны библиотеки из образа.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pvz ./cmd/pvz

# distroless: ни shell, ни пакетного менеджера, только бинарник и сертификаты.
# Тег nonroot: процесс работает не от root. Healthcheck в compose вызывает
# сам бинарник (pvz healthcheck), wget и curl тут нет.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/pvz /usr/local/bin/pvz
EXPOSE 8080 3000
ENTRYPOINT ["pvz"]
CMD ["serve"]
