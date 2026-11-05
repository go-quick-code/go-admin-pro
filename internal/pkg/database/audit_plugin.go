package database

import (
	"context"
	"reflect"
	"strconv"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"gorm.io/gorm"
)

// SystemAccount 是无登录上下文时记录的审计账号（系统）。
const SystemAccount = "0"

// AuditPlugin 是 GORM 审计插件：写入时自动回填 creator/updater。
//
// 当前用户从请求上下文（authn.Principal）获取，取不到时记为系统账号 "0"。
// 创建时 creator 与 updater 同值；更新时只动 updater（最后修改人）。
// 已显式赋值的 creator 不覆盖；模型没有对应字段（如关联表）时自动跳过。
type AuditPlugin struct{}

// NewAuditPlugin 创建审计插件。
func NewAuditPlugin() *AuditPlugin {
	return &AuditPlugin{}
}

// Name 实现 gorm.Plugin 接口。
func (p *AuditPlugin) Name() string { return "go-admin-pro:audit" }

// Initialize 在创建与更新回调前注册审计回填。
func (p *AuditPlugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	if err := cb.Create().Before("gorm:create").Register("audit:create", p.createCallback); err != nil {
		return err
	}
	return cb.Update().Before("gorm:update").Register("audit:update", p.updateCallback)
}

func (p *AuditPlugin) createCallback(db *gorm.DB) {
	user := auditUser(db.Statement.Context)
	// Create/Updates 以 map 为目标时，写入列由 map 决定，直接注入。
	if dest, ok := db.Statement.Dest.(map[string]interface{}); ok {
		injectMapColumn(db, dest, "creator", user)
		injectMapColumn(db, dest, "updater", user)
		return
	}
	switch rv := reflectValue(db); rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			setAuditFields(rv.Index(i), user, true)
		}
	case reflect.Struct:
		setAuditFields(rv, user, true)
	}
}

func (p *AuditPlugin) updateCallback(db *gorm.DB) {
	user := auditUser(db.Statement.Context)
	if dest, ok := db.Statement.Dest.(map[string]interface{}); ok {
		injectMapColumn(db, dest, "updater", user)
		return
	}
	switch rv := reflectValue(db); rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			setAuditFields(rv.Index(i), user, false)
		}
	case reflect.Struct:
		setAuditFields(rv, user, false)
	}
}

// reflectValue 返回本次操作的目标值；无法取得时返回零值。
func reflectValue(db *gorm.DB) reflect.Value {
	if db.Statement == nil {
		return reflect.Value{}
	}
	return db.Statement.ReflectValue
}

// auditUser 返回当前登录用户 ID 的字符串形式，取不到时返回系统账号。
func auditUser(ctx context.Context) string {
	principal, ok := authn.From(ctx)
	if !ok || principal.UserID <= 0 {
		return SystemAccount
	}
	return strconv.FormatInt(principal.UserID, 10)
}

// setAuditFields 回填结构体上的审计字段。
// 创建时 creator 与 updater 同值且不覆盖显式赋值；更新时 updater 总是覆盖为最后修改人。
func setAuditFields(v reflect.Value, user string, creating bool) {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	if creating {
		setStringFieldIfEmpty(v, "Creator", user)
		setStringFieldIfEmpty(v, "Updater", user)
		return
	}
	setStringField(v, "Updater", user)
}

func setStringFieldIfEmpty(v reflect.Value, name, value string) {
	field := v.FieldByName(name)
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.String && field.String() == "" {
		field.SetString(value)
	}
}

func setStringField(v reflect.Value, name, value string) {
	field := v.FieldByName(name)
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
		field.SetString(value)
	}
}

// injectMapColumn 把审计列注入 Create/Updates 的 map 目标列。
// 仅当模型 schema 声明了该列时注入，避免影响没有审计字段的表；
// map 中已有同名列表示调用方显式指定，不覆盖。
func injectMapColumn(db *gorm.DB, dest map[string]interface{}, column, user string) {
	if db.Statement == nil || db.Statement.Schema == nil {
		return
	}
	if _, ok := db.Statement.Schema.FieldsByDBName[column]; !ok {
		return
	}
	if _, exists := dest[column]; exists {
		return
	}
	dest[column] = user
}
