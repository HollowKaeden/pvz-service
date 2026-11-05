// Package billing встраивает только миграции отдельной БД начислений.
package billing

import "embed"

// SQL — миграции billing.
//
//go:embed *.sql
var SQL embed.FS
