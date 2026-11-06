package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/dbschema"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ErrDBDocPlatformOnly 是数据库文档越权访问的哨兵错误（非平台租户 403）。
var ErrDBDocPlatformOnly = errors.New("数据库文档仅平台租户可访问")

// dbDocExcludedTables 是文档不呈现的非业务表：goose 是迁移工具的版本账本，
// 不属于业务 schema（spec §4.9.1）。
var dbDocExcludedTables = []string{"goose_db_version"}

// requireDBDocPlatform 校验当前租户是平台租户。数据库文档暴露整个库的
// schema（含全部租户的表结构），是平台级能力：非平台租户即使持有
// infra:db-doc:query 权限码也一律 403（spec §4.9.4，与 message 渠道同模式）。
func requireDBDocPlatform(ctx context.Context) error {
	if tenant.ID(ctx) != tenant.DefaultID {
		return xerr.Wrap(ErrDBDocPlatformOnly, http.StatusForbidden, http.StatusForbidden, ErrDBDocPlatformOnly.Error())
	}
	return nil
}

// ListDBTables 列出当前库的业务表（含 system_*/infra_*，排除 goose_db_version），
// keyword 按表名/注释模糊过滤（spec §4.9.1）。
func (s *Service) ListDBTables(ctx context.Context, keyword string) ([]dbschema.TableInfo, error) {
	if err := requireDBDocPlatform(ctx); err != nil {
		return nil, err
	}
	return dbschema.ListTables(ctx, s.repo.DB(), dbschema.ListOptions{
		Keyword:       keyword,
		ExcludeTables: dbDocExcludedTables,
	})
}

// GetDBTable 返回单表结构（列名/类型/可空/主键/自增/默认值/注释/序号，
// spec §4.9.2）。不存在的表映射 404，非法表名映射 400。
func (s *Service) GetDBTable(ctx context.Context, tableName string) (*dbschema.TableInfo, error) {
	if err := requireDBDocPlatform(ctx); err != nil {
		return nil, err
	}
	table, err := dbschema.GetTable(ctx, s.repo.DB(), tableName)
	if err != nil {
		switch {
		case errors.Is(err, dbschema.ErrInvalidTableName):
			return nil, xerr.BadRequest(err.Error())
		case errors.Is(err, dbschema.ErrTableNotFound):
			return nil, xerr.NotFound(err.Error())
		default:
			return nil, xerr.Internal(err)
		}
	}
	return table, nil
}

// ExportDBDoc 生成全库 Markdown 文档：含生成时间、数据库名与每张表的说明
// 和列清单（spec §4.9.3）。文件名 db-doc-YYYYMMDD-HHmmss.md，按业务时区
// （ADR 0006）取当前时间。
func (s *Service) ExportDBDoc(ctx context.Context) ([]byte, string, error) {
	if err := requireDBDocPlatform(ctx); err != nil {
		return nil, "", err
	}
	tables, err := dbschema.ListTables(ctx, s.repo.DB(), dbschema.ListOptions{
		ExcludeTables: dbDocExcludedTables,
	})
	if err != nil {
		return nil, "", xerr.Internal(err)
	}

	now := time.Now().In(s.cfg.Location())
	dbName, err := dbschema.CurrentDatabase(ctx, s.repo.DB())
	if err != nil {
		return nil, "", xerr.Internal(err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# 数据库文档\n\n")
	fmt.Fprintf(&b, "- 生成时间：%s\n", now.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- 数据库：%s\n", dbName)
	fmt.Fprintf(&b, "- 表数量：%d\n\n", len(tables))

	for _, t := range tables {
		table, err := dbschema.GetTable(ctx, s.repo.DB(), t.Name)
		if err != nil {
			return nil, "", xerr.Internal(err)
		}
		if table.Comment != "" {
			fmt.Fprintf(&b, "## %s（%s）\n\n", table.Name, table.Comment)
		} else {
			fmt.Fprintf(&b, "## %s\n\n", table.Name)
		}
		b.WriteString("| 序号 | 列名 | 类型 | 可空 | 主键 | 自增 | 默认值 | 注释 |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, col := range table.Columns {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s | %s |\n",
				col.Ordinal,
				mdCell(col.Name),
				mdCell(col.ColumnType),
				yesNo(col.Nullable),
				yesNo(col.PrimaryKey),
				yesNo(col.AutoIncrement),
				mdCell(deref(col.DefaultValue)),
				mdCell(col.Comment),
			)
		}
		b.WriteString("\n")
	}

	filename := "db-doc-" + now.Format("20060102-150405") + ".md"
	return []byte(b.String()), filename, nil
}

// yesNo 把布尔渲染为中文「是/否」，nil 指针（默认值 NULL）渲染为空。
func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// mdCell 转义 Markdown 表格单元格：竖线与换行会破坏表格结构。
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
