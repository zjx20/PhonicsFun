// Package web embeds the built frontend (web/dist, produced by `make web`).
//
// dist 里始终有一个 .gitkeep 占位，保证前端未构建时 go build 也能通过；
// 此时运行服务会在页面上提示先执行 make web。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
