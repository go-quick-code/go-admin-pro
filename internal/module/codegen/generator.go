package codegen

import (
	"context"
	"regexp"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/dbschema"
)

// TableInfo/ColumnInfo 是 dbschema 的别名（issue #83 抽取）：读取逻辑统一收敛
// 到 internal/pkg/dbschema，codegen 保留原方法签名与 JSON 契约，行为不变。
type (
	TableInfo  = dbschema.TableInfo
	ColumnInfo = dbschema.ColumnInfo
)

// Generator 基于 information_schema 读取表结构并生成代码。
type Generator struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Generator { return &Generator{db: db} }

var (
	// tableNameRe 供生成参数校验复用（schema 读取侧的同名正则已移入 dbschema）。
	tableNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	moduleRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	businessRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// ListTables 查询当前库中的业务表（排除系统表）。
func (g *Generator) ListTables(ctx context.Context, keyword string) ([]TableInfo, error) {
	return dbschema.ListTables(ctx, g.db, dbschema.ListOptions{
		Keyword: keyword,
		// 原 generator.go 的 WHERE 条件原样迁移：三类系统前缀不进入生成列表。
		ExcludePrefixes: []string{`system\_`, `infra\_`, `gen\_`},
	})
}

// GetTable 查询单表结构。
func (g *Generator) GetTable(ctx context.Context, tableName string) (*TableInfo, error) {
	return dbschema.GetTable(ctx, g.db, tableName)
}
