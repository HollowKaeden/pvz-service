// Package migrations встраивает SQL-миграции в бинарник: сервис применяет их
// сам (подкоманда migrate или MIGRATE_ON_START), отдельный CLI goose не нужен.
package migrations

import "embed"

// PVZ — миграции БД pvz.
//
//go:embed pvz/*.sql
var PVZ embed.FS
