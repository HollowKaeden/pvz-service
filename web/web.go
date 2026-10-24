// Package web — фронтенд pvz: HTML, CSS и JS-модули без сборки и без npm.
// Файлы встроены в бинарник, HTTP-слой отдаёт страницу на / и файлы на
// /assets/. Своих ручек у фронтенда нет: он ходит в тот же API, что описан в
// контракте.
package web

import "embed"

// FS — index.html и папка assets.
//
//go:embed index.html assets
var FS embed.FS
