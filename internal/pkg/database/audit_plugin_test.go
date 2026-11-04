package database_test

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lautung/go-admin-pro/internal/pkg/authn"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"gorm.io/gorm"
)

// auditRecord 模拟带审计字段的实体。
type auditRecord struct {
	ID      int64 `gorm:"primaryKey;autoIncrement"`
	Name    string
	Creator string `gorm:"size:64;not null;default:'0'"`
	Updater string `gorm:"size:64;not null;default:'0'"`
}

// plainRecord 模拟没有审计字段的表（如关联表）。
type plainRecord struct {
	ID   int64 `gorm:"primaryKey;autoIncrement"`
	Name string
}

func newAuditDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	if err := db.Use(database.NewAuditPlugin()); err != nil {
		t.Fatalf("安装审计插件失败: %v", err)
	}
	if err := db.AutoMigrate(&auditRecord{}, &plainRecord{}); err != nil {
		t.Fatalf("建测试表失败: %v", err)
	}
	return db
}

func withUser(ctx context.Context, id int64) context.Context {
	return authn.With(ctx, authn.Principal{UserID: id, TenantID: 1, Username: "tester"})
}

func mustGetRecord(t *testing.T, db *gorm.DB, id int64) auditRecord {
	t.Helper()
	var rec auditRecord
	if err := db.First(&rec, id).Error; err != nil {
		t.Fatalf("查询记录 %d 失败: %v", id, err)
	}
	return rec
}

func TestAuditPluginCreateFillsCreatorAndUpdater(t *testing.T) {
	db := newAuditDB(t)
	rec := auditRecord{Name: "创建"}
	if err := db.WithContext(withUser(context.Background(), 7)).Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	got := mustGetRecord(t, db, rec.ID)
	if got.Creator != "7" || got.Updater != "7" {
		t.Errorf("创建后 creator/updater = %q/%q，期望均为 7", got.Creator, got.Updater)
	}
}

func TestAuditPluginCreateWithoutPrincipalUsesSystemAccount(t *testing.T) {
	db := newAuditDB(t)
	rec := auditRecord{Name: "系统写入"}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	got := mustGetRecord(t, db, rec.ID)
	if got.Creator != "0" || got.Updater != "0" {
		t.Errorf("无登录上下文时 creator/updater = %q/%q，期望均为 0（系统账号）", got.Creator, got.Updater)
	}
}

func TestAuditPluginCreateRespectsExplicitValues(t *testing.T) {
	db := newAuditDB(t)
	rec := auditRecord{Name: "显式赋值", Creator: "42", Updater: "42"}
	if err := db.WithContext(withUser(context.Background(), 7)).Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	got := mustGetRecord(t, db, rec.ID)
	if got.Creator != "42" || got.Updater != "42" {
		t.Errorf("显式赋值被覆盖：creator/updater = %q/%q，期望均为 42", got.Creator, got.Updater)
	}
}

func TestAuditPluginBatchCreate(t *testing.T) {
	db := newAuditDB(t)
	recs := []auditRecord{{Name: "a"}, {Name: "b"}}
	if err := db.WithContext(withUser(context.Background(), 9)).Create(&recs).Error; err != nil {
		t.Fatalf("批量创建失败: %v", err)
	}
	for _, rec := range recs {
		got := mustGetRecord(t, db, rec.ID)
		if got.Creator != "9" || got.Updater != "9" {
			t.Errorf("记录 %d creator/updater = %q/%q，期望均为 9", got.ID, got.Creator, got.Updater)
		}
	}
}

func TestAuditPluginStructUpdateOnlyTouchesUpdater(t *testing.T) {
	db := newAuditDB(t)
	rec := auditRecord{Name: "原始"}
	if err := db.WithContext(withUser(context.Background(), 7)).Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	rec.Name = "修改"
	if err := db.WithContext(withUser(context.Background(), 8)).Save(&rec).Error; err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got := mustGetRecord(t, db, rec.ID)
	if got.Updater != "8" {
		t.Errorf("更新后 updater = %q，期望 8", got.Updater)
	}
	if got.Creator != "7" {
		t.Errorf("更新不应修改 creator：got %q，期望 7", got.Creator)
	}
}

func TestAuditPluginMapUpdateInjectsUpdater(t *testing.T) {
	db := newAuditDB(t)
	rec := auditRecord{Name: "原始"}
	if err := db.WithContext(withUser(context.Background(), 7)).Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if err := db.WithContext(withUser(context.Background(), 8)).
		Model(&auditRecord{}).Where("id = ?", rec.ID).
		Updates(map[string]interface{}{"name": "修改"}).Error; err != nil {
		t.Fatalf("按列更新失败: %v", err)
	}
	got := mustGetRecord(t, db, rec.ID)
	if got.Updater != "8" {
		t.Errorf("按列更新后 updater = %q，期望 8", got.Updater)
	}
	if got.Creator != "7" {
		t.Errorf("按列更新不应修改 creator：got %q，期望 7", got.Creator)
	}
}

func TestAuditPluginUpdateWithoutPrincipalUsesSystemAccount(t *testing.T) {
	db := newAuditDB(t)
	rec := auditRecord{Name: "原始"}
	if err := db.WithContext(withUser(context.Background(), 7)).Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if err := db.Model(&auditRecord{}).Where("id = ?", rec.ID).
		Updates(map[string]interface{}{"name": "系统修改"}).Error; err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got := mustGetRecord(t, db, rec.ID)
	if got.Updater != "0" {
		t.Errorf("无登录上下文更新后 updater = %q，期望 0（系统账号）", got.Updater)
	}
}

func TestAuditPluginSkipsModelsWithoutAuditFields(t *testing.T) {
	db := newAuditDB(t)
	rec := plainRecord{Name: "无审计字段"}
	if err := db.WithContext(withUser(context.Background(), 7)).Create(&rec).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := db.Model(&plainRecord{}).Where("id = ?", rec.ID).
		Updates(map[string]interface{}{"name": "更新"}).Error; err != nil {
		t.Fatalf("更新失败: %v", err)
	}
}
