package model_test

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/lautung/go-admin-pro/internal/module/infra/model"
)

// TestInfraEntityModelsEmbedBaseEntity 验证 infra 模块实体模型共享统一底座：
// id / created_at / updated_at / deleted_at / creator / updater。
func TestInfraEntityModelsEmbedBaseEntity(t *testing.T) {
	models := []any{
		model.File{},
		model.Config{},
		model.Job{},
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

// TestJobLogIsAppendOnly 验证任务日志是追加型表：只有 created_at，无软删除。
func TestJobLogIsAppendOnly(t *testing.T) {
	s, err := schema.Parse(model.JobLog{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析 JobLog 模型失败: %v", err)
	}
	if _, ok := s.FieldsByDBName["created_at"]; !ok {
		t.Errorf("JobLog 缺少 created_at")
	}
	if _, ok := s.FieldsByDBName["deleted_at"]; ok {
		t.Errorf("JobLog 是追加型日志表，不应包含软删除字段")
	}
}

// TestFileHasBizColumns 验证 infra_file 的通用业务归属列（issue #17）：
// biz_type（字符串，空串表示未关联业务）与 biz_id（整数，0 表示未关联业务）。
func TestFileHasBizColumns(t *testing.T) {
	s, err := schema.Parse(model.File{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析 File 模型失败: %v", err)
	}
	bizType, ok := s.FieldsByDBName["biz_type"]
	if !ok {
		t.Fatal("File 缺少 biz_type 列")
	}
	if bizType.FieldType.Kind() != reflect.String {
		t.Errorf("biz_type 类型为 %v，期望 string", bizType.FieldType)
	}
	bizID, ok := s.FieldsByDBName["biz_id"]
	if !ok {
		t.Fatal("File 缺少 biz_id 列")
	}
	if bizID.FieldType.Kind() != reflect.Int64 {
		t.Errorf("biz_id 类型为 %v，期望 int64", bizID.FieldType)
	}
}
