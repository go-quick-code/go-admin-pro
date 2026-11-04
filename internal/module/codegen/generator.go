package codegen

import (
	"context"
	"database/sql"
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

// Generator 基于 information_schema 读取表结构并生成代码。
type Generator struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Generator { return &Generator{db: db} }

var (
	tableNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	moduleRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	businessRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// ListTables 查询当前库中的业务表（排除系统表）。
func (g *Generator) ListTables(ctx context.Context, keyword string) ([]TableInfo, error) {
	sqlText := `SELECT table_name, IFNULL(table_comment, ''), IFNULL(DATE_FORMAT(create_time, '%Y-%m-%d %H:%i:%s'), ''),
		IFNULL(DATE_FORMAT(update_time, '%Y-%m-%d %H:%i:%s'), '')
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name NOT LIKE 'system\_%'
		  AND table_name NOT LIKE 'infra\_%'
		  AND table_name NOT LIKE 'gen\_%'`
	args := make([]interface{}, 0, 2)
	if keyword != "" {
		sqlText += ` AND (table_name LIKE ? OR table_comment LIKE ?)`
		args = append(args, "%"+keyword+"%", "%"+keyword+"%")
	}
	sqlText += ` ORDER BY table_name ASC`

	rows, err := g.db.WithContext(ctx).Raw(sqlText, args...).Rows()
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

// GetTable 查询单表结构。
func (g *Generator) GetTable(ctx context.Context, tableName string) (*TableInfo, error) {
	if !tableNameRe.MatchString(tableName) {
		return nil, fmt.Errorf("非法的表名")
	}
	var table TableInfo
	row := g.db.WithContext(ctx).Raw(`SELECT table_name, IFNULL(table_comment, '')
		FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, tableName).Row()
	if err := row.Scan(&table.Name, &table.Comment); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("表不存在：%s", tableName)
		}
		return nil, err
	}

	rows, err := g.db.WithContext(ctx).Raw(`SELECT column_name, IFNULL(column_comment, ''), data_type, column_type,
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
