// Package dbschema 是数据库 schema 读取层（issue #83，spec §4.9）：
// 从 codegen generator.go 抽取的 information_schema 查询逻辑，供代码生成
// （排除系统表）与 infra 数据库文档（含 system_/infra_、排除 goose_db_version）
// 共用。只做读取，不做过滤策略决策——排除规则由调用方通过 ListOptions 声明。
package dbschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// TableInfo 是数据库表信息。
type TableInfo struct {
	Name       string       `json:"name"`
	Comment    string       `json:"comment"`
	CreateTime string       `json:"createTime"`
	UpdateTime string       `json:"updateTime"`
	Columns    []ColumnInfo `json:"columns,omitempty"`
}

// ColumnInfo 是列信息。
type ColumnInfo struct {
	Name          string  `json:"name"`
	Comment       string  `json:"comment"`
	DataType      string  `json:"dataType"`
	ColumnType    string  `json:"columnType"`
	Nullable      bool    `json:"nullable"`
	PrimaryKey    bool    `json:"primaryKey"`
	AutoIncrement bool    `json:"autoIncrement"`
	DefaultValue  *string `json:"defaultValue"`
	Ordinal       int     `json:"ordinal"`
}

// ErrTableNotFound 是 GetTable 查不到表的哨兵错误，文案与 codegen 原实现
// 一致（表不存在：<名>），调用方可用 errors.Is 区分 404 与内部错误。
var ErrTableNotFound = errors.New("表不存在")

// ErrInvalidTableName 是表名未通过字符白名单（字母/数字/下划线）的哨兵错误，
// 文案与 codegen 原实现一致，调用方据此映射 400。
var ErrInvalidTableName = errors.New("非法的表名")

// tableNameRe 限制表名为字母/数字/下划线，防注入（原 codegen 校验，行为不变）。
var tableNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// ListOptions 是表清单的过滤条件：零值表示不过滤，返回当前库全部表。
type ListOptions struct {
	// Keyword 按表名/表注释模糊匹配，空为不过滤。
	Keyword string
	// ExcludePrefixes 是 LIKE 前缀排除（调用方只给前缀本身，下划线需转义，
	// 如 `system\_`；尾部的 `%` 由实现补齐）。
	ExcludePrefixes []string
	// ExcludeTables 是精确表名排除（如 goose_db_version）。
	ExcludeTables []string
}

// ListTables 查询当前库的表清单（不含列），按表名升序。
func ListTables(ctx context.Context, db *gorm.DB, opts ListOptions) ([]TableInfo, error) {
	sqlText := `SELECT table_name, IFNULL(table_comment, ''), IFNULL(DATE_FORMAT(create_time, '%Y-%m-%d %H:%i:%s'), ''),
		IFNULL(DATE_FORMAT(update_time, '%Y-%m-%d %H:%i:%s'), '')
		FROM information_schema.tables
		WHERE table_schema = DATABASE()`
	args := make([]interface{}, 0, 2+len(opts.ExcludePrefixes)+len(opts.ExcludeTables))
	for _, prefix := range opts.ExcludePrefixes {
		sqlText += ` AND table_name NOT LIKE ?`
		args = append(args, prefix+"%")
	}
	for _, name := range opts.ExcludeTables {
		sqlText += ` AND table_name <> ?`
		args = append(args, name)
	}
	if opts.Keyword != "" {
		sqlText += ` AND (table_name LIKE ? OR table_comment LIKE ?)`
		args = append(args, "%"+opts.Keyword+"%", "%"+opts.Keyword+"%")
	}
	sqlText += ` ORDER BY table_name ASC`

	rows, err := db.WithContext(ctx).Raw(sqlText, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]TableInfo, 0)
	for rows.Next() {
		var item TableInfo
		if err := rows.Scan(&item.Name, &item.Comment, &item.CreateTime, &item.UpdateTime); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

// CurrentDatabase 返回当前连接的数据库名（DATABASE()），供文档标注来源库。
func CurrentDatabase(ctx context.Context, db *gorm.DB) (string, error) {
	var name string
	if err := db.WithContext(ctx).Raw(`SELECT DATABASE()`).Row().Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

// GetTable 查询单表结构（含全部列，按序号升序）。
func GetTable(ctx context.Context, db *gorm.DB, tableName string) (*TableInfo, error) {
	if !tableNameRe.MatchString(tableName) {
		return nil, ErrInvalidTableName
	}
	var table TableInfo
	row := db.WithContext(ctx).Raw(`SELECT table_name, IFNULL(table_comment, '')
		FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, tableName).Row()
	if err := row.Scan(&table.Name, &table.Comment); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w：%s", ErrTableNotFound, tableName)
		}
		return nil, err
	}

	rows, err := db.WithContext(ctx).Raw(`SELECT column_name, IFNULL(column_comment, ''), data_type, column_type,
		is_nullable = 'YES', column_key = 'PRI', extra LIKE '%auto_increment%', column_default, ordinal_position
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ?
		ORDER BY ordinal_position ASC`, tableName).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var col ColumnInfo
		var defaultValue sql.NullString
		if err := rows.Scan(&col.Name, &col.Comment, &col.DataType, &col.ColumnType,
			&col.Nullable, &col.PrimaryKey, &col.AutoIncrement, &defaultValue, &col.Ordinal); err != nil {
			return nil, err
		}
		if defaultValue.Valid {
			v := defaultValue.String
			col.DefaultValue = &v
		}
		col.Name = strings.TrimSpace(col.Name)
		table.Columns = append(table.Columns, col)
	}
	return &table, rows.Err()
}
