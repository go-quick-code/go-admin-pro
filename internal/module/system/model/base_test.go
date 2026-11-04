package model_test

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/lautung/go-admin-pro/internal/module/system/model"
)

// TestEntityModelsEmbedBaseEntity 验证 system 模块所有实体模型共享统一底座：
// id / created_at / updated_at / deleted_at / creator / updater。
func TestEntityModelsEmbedBaseEntity(t *testing.T) {
	models := []any{
		model.Tenant{},
		model.User{},
		model.Role{},
		model.Menu{},
		model.Dept{},
		model.Post{},
		model.DictType{},
		model.DictData{},
		model.LoginLog{},
		model.OperateLog{},
	}

	deletedAtType := reflect.TypeOf(gorm.DeletedAt{})
	for _, m := range models {
		s, err := schema.Parse(m, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("解析 %T 模型失败: %v", m, err)
		}

		for _, col := range []string{"id", "created_at", "updated_at", "deleted_at", "creator", "updater"} {
			if _, ok := s.FieldsByDBName[col]; !ok {
				t.Errorf("%T 缺少底座字段 %s", m, col)
			}
		}

		if f := s.FieldsByDBName["deleted_at"]; f != nil && f.FieldType != deletedAtType {
			t.Errorf("%T 的 deleted_at 类型为 %v，期望 gorm.DeletedAt（软删除行为保持不变）", m, f.FieldType)
		}
		for _, col := range []string{"creator", "updater"} {
			if f := s.FieldsByDBName[col]; f != nil && f.FieldType.Kind() != reflect.String {
				t.Errorf("%T 的 %s 类型为 %v，期望 string", m, col, f.FieldType)
			}
		}
	}
}

// TestJoinModelsUseTenantID 验证关联表模型补 tenant_id，且不设软删除。
func TestJoinModelsUseTenantID(t *testing.T) {
	models := []any{
		model.UserRole{},
		model.UserPost{},
		model.RoleMenu{},
		model.RoleDept{},
	}

	for _, m := range models {
		s, err := schema.Parse(m, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("解析 %T 模型失败: %v", m, err)
		}

		if _, ok := s.FieldsByDBName["tenant_id"]; !ok {
			t.Errorf("%T 缺少 tenant_id 字段", m)
		}
		if _, ok := s.FieldsByDBName["deleted_at"]; ok {
			t.Errorf("%T 不应包含软删除字段（关联表不设软删除，变更由操作日志记录）", m)
		}
	}
}

// TestDataScopeEnumAligned 验证角色数据范围枚举对齐芋道 1–5（issue #5）：
// 1 全部 / 2 自定义部门 / 3 本部门 / 4 本部门及以下 / 5 仅本人。
func TestDataScopeEnumAligned(t *testing.T) {
	cases := map[string]struct {
		got  int8
		want int8
	}{
		"全部数据":   {model.DataScopeAll, 1},
		"自定义部门":  {model.DataScopeCustom, 2},
		"本部门":    {model.DataScopeDept, 3},
		"本部门及以下": {model.DataScopeDeptAndSub, 4},
		"仅本人":    {model.DataScopeSelf, 5},
	}
	for name, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s 常量 = %d，期望 %d", name, tc.got, tc.want)
		}
	}
}
