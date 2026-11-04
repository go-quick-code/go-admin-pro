package database

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TenantPlugin 是 GORM 多租户插件。
//
// 对注册过的租户表，在查询/更新/删除时自动追加 tenant_id 条件，
// 调用方无需在每条 SQL 上重复拼装。系统级操作可通过 tenant.Skip(db) 跳过。
type TenantPlugin struct {
	tables map[string]struct{}
}

// NewTenantPlugin 创建插件，tables 为需要租户隔离的表名。
func NewTenantPlugin(tables []string) *TenantPlugin {
	m := make(map[string]struct{}, len(tables))
	for _, t := range tables {
		m[t] = struct{}{}
	}
	return &TenantPlugin{tables: m}
}

func (p *TenantPlugin) Name() string { return "go-admin-pro:tenant" }

func (p *TenantPlugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()

	if err := cb.Query().Before("gorm:query").Register("tenant:query", p.queryCallback); err != nil {
		return err
	}
	if err := cb.Update().Before("gorm:update").Register("tenant:update", p.writeCallback); err != nil {
		return err
	}
	if err := cb.Delete().Before("gorm:delete").Register("tenant:delete", p.writeCallback); err != nil {
		return err
	}
	if err := cb.Create().Before("gorm:create").Register("tenant:create", p.createCallback); err != nil {
		return err
	}
	return nil
}

func (p *TenantPlugin) table(db *gorm.DB) (string, bool) {
	if db.Statement == nil {
		return "", false
	}
	table := db.Statement.Table
	if table == "" && db.Statement.Schema != nil {
		table = db.Statement.Schema.Table
	}
	if table == "" {
		return "", false
	}
	table = strings.Trim(table, "`")
	_, ok := p.tables[table]
	return table, ok
}

// guard 是 fail-closed 守卫：
//   - 未登记的表：放行（返回 false，插件不做任何事）；
//   - 显式 tenant.Skip：放行（返回 false）；
//   - 缺少租户上下文：注入 ErrNoTenantContext 并中止操作（返回 false）；
//   - 其余：返回 true，由回调追加租户条件 / 回填 tenant_id。
//
// 系统级操作（seed、定时任务、跨租户统计等）必须显式 tenant.Skip，
// 插件不再把「没有租户上下文」当作系统级的隐式信号。
func (p *TenantPlugin) guard(db *gorm.DB) bool {
	table, ok := p.table(db)
	if !ok {
		return false
	}
	if tenant.IsSkipped(db) {
		return false
	}
	if _, has := tenant.FromContext(db.Statement.Context); !has {
		db.AddError(fmt.Errorf("访问租户表 %s：%w（系统级操作请用 tenant.Skip 显式跳过）", table, tenant.ErrNoTenantContext))
		return false
	}
	return true
}

func (p *TenantPlugin) queryCallback(db *gorm.DB) {
	if !p.guard(db) {
		return
	}
	id := tenant.ID(db.Statement.Context)
	db.Statement.AddClause(clause.Where{Exprs: []clause.Expression{tenantClause(id)}})
}

func (p *TenantPlugin) writeCallback(db *gorm.DB) {
	if !p.guard(db) {
		return
	}
	id := tenant.ID(db.Statement.Context)
	db.Statement.AddClause(clause.Where{Exprs: []clause.Expression{tenantClause(id)}})
}

// createCallback 在插入时自动回填 tenant_id，避免各业务层重复赋值。
func (p *TenantPlugin) createCallback(db *gorm.DB) {
	if !p.guard(db) {
		return
	}
	id := tenant.ID(db.Statement.Context)
	rv := db.Statement.ReflectValue
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			setTenantID(rv.Index(i), id)
		}
	case reflect.Struct:
		setTenantID(rv, id)
	case reflect.Ptr:
		if !rv.IsNil() {
			setTenantID(rv.Elem(), id)
		}
	}
}

func setTenantID(v reflect.Value, id int64) {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	field := v.FieldByName("TenantID")
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.Int64 && field.Int() == 0 {
		field.SetInt(id)
	}
}
