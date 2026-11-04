// Package migrations holds the goose SQL migrations embedded into the binary.
package migrations

import "embed"

// FS 是内嵌的迁移文件系统，供 goose 读取。
//
//go:embed *.sql
var FS embed.FS
