package database

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
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
// 已登记表若模型没有可写的 TenantID 字段，直接报错：否则 INSERT 会省略
// tenant_id 并落到列默认值，数据静默写入默认租户（ADR 0019）。
func (p *TenantPlugin) createCallback(db *gorm.DB) {
	if !p.guard(db) {
		return
	}
	id := tenant.ID(db.Statement.Context)
	if fillTenantID(db.Statement.ReflectValue, id) {
		return
	}
	table, _ := p.table(db)
	if isMapReflectValue(db.Statement.ReflectValue) {
		db.AddError(fmt.Errorf("写入租户表 %s：Create 的 dest 是 map，租户插件无法自动回填 tenant_id；请改用结构体或 tenant.Skip 显式跳过", table))
		return
	}
	db.AddError(fmt.Errorf("写入租户表 %s：模型缺少可写的 TenantID 字段（系统级写入请用 tenant.Skip 显式跳过）", table))
}

// fillTenantID 回填租户 ID，并报告模型是否具备可写的 TenantID 字段。
// 支持结构体、指针、结构体切片/数组与空切片（按元素类型判断）。
func fillTenantID(v reflect.Value, id int64) bool {
	if !v.IsValid() {
		return false
	}
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return typeHasTenantID(v.Type().Elem())
		}
		ok := true
		for i := 0; i < v.Len(); i++ {
			if !fillTenantID(v.Index(i), id) {
				ok = false
			}
		}
		return ok
	case reflect.Struct:
		field := v.FieldByName("TenantID")
		if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Int64 {
			return false
		}
		if field.Int() == 0 {
			field.SetInt(id)
		}
		return true
	default:
		return false
	}
}

func typeHasTenantID(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		return typeHasTenantID(t.Elem())
	case reflect.Struct:
		field, ok := t.FieldByName("TenantID")
		return ok && field.Type.Kind() == reflect.Int64
	default:
		// interface、map 等无法确定元素/键类型，按不可回填处理（fail-closed）。
		return false
	}
}

// isMapReflectValue 判断 Create 的 dest 是否为 map；map 无法按结构体字段回填。
func isMapReflectValue(v reflect.Value) bool {
	for v.IsValid() && (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	return v.IsValid() && v.Kind() == reflect.Map
}
