// Package oapi — код, сгенерированный oapi-codegen из api/openapi.yaml: DTO
// запросов и ответов, роутинг на net/http и strict-интерфейс ручек.
//
// Руками здесь ничего не правится: меняется контракт или oapi-codegen.yaml,
// затем make generate. Типы этого пакета не выходят за пределы transport/http.
package oapi

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../../api/openapi.yaml
