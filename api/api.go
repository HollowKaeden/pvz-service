// Package api встраивает файлы контракта в бинарник, чтобы сервис отдавал
// спеку Swagger UI без внешних файлов рядом с ним.
package api

import _ "embed"

// OpenAPI — HTTP-контракт pvz (openapi.yaml, копия swagger.yaml из задания).
//
//go:embed openapi.yaml
var OpenAPI []byte
