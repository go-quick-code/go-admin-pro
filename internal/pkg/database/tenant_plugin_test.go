package database_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// tenantRow 是插件单测的最小租户表模型，映射到 TenantTables 登记过的 system_user。
type tenantRow struct {
	ID       int64  `gorm:"primaryKey"`
	TenantID int64  `gorm:"index;not null;default:1"`
	Name     string `gorm:"size:64"`
}

func (tenantRow) TableName() string { return "system_user" }

// plainRow 是未登记的普通表模型：无论有无租户上下文都不应被插件拦截。
type plainRow struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

func (plainRow) TableName() string { return "system_menu" }

// tenantRowMissingTenantID 映射到已登记的 system_user，但模型漏了 TenantID：
// 写入必须 fail-closed，不能静默落到列默认租户（ADR 0019）。
type tenantRowMissingTenantID struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

func (tenantRowMissingTenantID) TableName() string { return "system_user" }

// newPluginDB 建立装好租户插件的独立 sqlite 库，并写入两个租户的行。
// 限制单连接：内存库按连接隔离，必须让所有语句走同一个连接。
func newPluginDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取 sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&tenantRow{}, &plainRow{}); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}

	seed := []tenantRow{
		{ID: 1, TenantID: 1, Name: "tenant1-a"},
		{ID: 2, TenantID: 1, Name: "tenant1-b"},
		{ID: 3, TenantID: 2, Name: "tenant2-a"},
	}
	if err := tenant.Skip(db).Create(&seed).Error; err != nil {
		t.Fatalf("写入种子行: %v", err)
	}
	return db
}

func countRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var total int64
	if err := tenant.Skip(db).Model(&tenantRow{}).Count(&total).Error; err != nil {
		t.Fatalf("计数: %v", err)
	}
	return total
}

// 无租户上下文查询租户表必须报错（fail-closed），而不是静默返回全表数据。
func TestTenantPluginFailsClosedWithoutTenantContext(t *testing.T) {
	db := newPluginDB(t)

	var rows []tenantRow
	err := db.Find(&rows).Error
	if err == nil {
		t.Fatalf("无租户上下文查询租户表：期望报错，实际返回 %d 行", len(rows))
	}
	if !errors.Is(err, tenant.ErrNoTenantContext) {
		t.Fatalf("无租户上下文查询租户表：期望哨兵错误 tenant.ErrNoTenantContext，实际 %v", err)
	}
}

// 带租户上下文的查询只返回本租户的行。
func TestTenantPluginFiltersByTenantContext(t *testing.T) {
	db := newPluginDB(t)

	ctx := tenant.WithTenantID(context.Background(), 2)
	var rows []tenantRow
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		t.Fatalf("带租户上下文查询: %v", err)
	}
	if len(rows) != 1 || rows[0].TenantID != 2 {
		t.Fatalf("租户 2 应只看到自己的 1 行，实际 %+v", rows)
	}
}

// tenant.Skip 显式跳过：系统级操作可以跨租户读写。
func TestTenantPluginExplicitSkipBypassesFilter(t *testing.T) {
	db := newPluginDB(t)

	var rows []tenantRow
	if err := tenant.Skip(db).Find(&rows).Error; err != nil {
		t.Fatalf("tenant.Skip 查询: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("tenant.Skip 应看到全部 3 行，实际 %d 行", len(rows))
	}
}

// 未登记的表不受插件影响：无租户上下文也正常查询。
func TestTenantPluginLeavesUnregisteredTablesAlone(t *testing.T) {
	db := newPluginDB(t)

	var rows []plainRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("未登记表的查询不应被拦截: %v", err)
	}
}

// 无租户上下文写入租户表必须报错且不落库；带上下文写入自动回填 tenant_id。
func TestTenantPluginCreateGuardAndBackfill(t *testing.T) {
	db := newPluginDB(t)

	err := db.Create(&tenantRow{ID: 10, Name: "no-ctx"}).Error
	if !errors.Is(err, tenant.ErrNoTenantContext) {
		t.Fatalf("无租户上下文写入租户表：期望 tenant.ErrNoTenantContext，实际 %v", err)
	}
	if got := countRows(t, db); got != 3 {
		t.Fatalf("失败的写入不应落库，期望 3 行，实际 %d 行", got)
	}

	ctx := tenant.WithTenantID(context.Background(), 3)
	row := tenantRow{ID: 11, Name: "with-ctx"}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("带租户上下文写入: %v", err)
	}
	if row.TenantID != 3 {
		t.Fatalf("插件应自动回填 tenant_id=3，实际 %d", row.TenantID)
	}
}

// 无租户上下文的更新与删除必须报错；带上下文的更新只影响本租户。
func TestTenantPluginUpdateDeleteGuard(t *testing.T) {
	db := newPluginDB(t)

	err := db.Model(&tenantRow{}).Where("id = ?", 1).Update("name", "hacked").Error
	if !errors.Is(err, tenant.ErrNoTenantContext) {
		t.Fatalf("无租户上下文更新租户表：期望 tenant.ErrNoTenantContext，实际 %v", err)
	}
	err = db.Delete(&tenantRow{}, 1).Error
	if !errors.Is(err, tenant.ErrNoTenantContext) {
		t.Fatalf("无租户上下文删除租户表：期望 tenant.ErrNoTenantContext，实际 %v", err)
	}

	ctx := tenant.WithTenantID(context.Background(), 2)
	if err := db.WithContext(ctx).Model(&tenantRow{}).Where("id = ?", 1).Update("name", "scoped").Error; err != nil {
		t.Fatalf("带租户上下文更新: %v", err)
	}
	var row tenantRow
	if err := tenant.Skip(db).First(&row, 1).Error; err != nil {
		t.Fatalf("回读租户 1 的行: %v", err)
	}
	if row.Name == "scoped" {
		t.Fatal("租户 2 的上下文不应改到租户 1 的行")
	}
}

// 事务内继续携带租户上下文：插件过滤在事务里同样生效。
func TestTenantPluginInsideTransactionKeepsTenantContext(t *testing.T) {
	db := newPluginDB(t)

	ctx := tenant.WithTenantID(context.Background(), 2)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []tenantRow
		if err := tx.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].TenantID != 2 {
			t.Fatalf("事务内租户过滤失效：期望租户 2 的 1 行，实际 %+v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("事务内查询: %v", err)
	}
}

// 已登记表的模型缺 TenantID 字段：插入必须报错且不落库，而不是写到默认租户。
func TestTenantPluginCreateFailsClosedWhenModelLacksTenantID(t *testing.T) {
	db := newPluginDB(t)

	ctx := tenant.WithTenantID(context.Background(), 3)
	err := db.WithContext(ctx).Create(&tenantRowMissingTenantID{ID: 20, Name: "no-field"}).Error
	if err == nil {
		t.Fatal("登记表模型缺 TenantID：期望报错，实际写入成功")
	}
	if !strings.Contains(err.Error(), "TenantID") {
		t.Fatalf("错误信息应点明缺少 TenantID 字段，实际 %v", err)
	}
	if got := countRows(t, db); got != 3 {
		t.Fatalf("失败的写入不应落库，期望 3 行，实际 %d 行", got)
	}
}

// interface 元素类型不能反射出 TenantID：必须 fail-closed，不能 panic。
func TestTenantPluginCreateEmptyInterfaceSliceFailsClosed(t *testing.T) {
	db := newPluginDB(t)

	ctx := tenant.WithTenantID(context.Background(), 3)
	err := db.WithContext(ctx).Table("system_user").Create(&[]any{}).Error
	if err == nil {
		t.Fatal("interface 切片 dest：期望报错，实际写入成功")
	}
	if !strings.Contains(err.Error(), "TenantID") {
		t.Fatalf("错误信息应点明无法回填 TenantID，实际 %v", err)
	}
}

// GORM 支持 map dest，但插件无法按字段回填：给出专门的错误文案并拒绝写入。
func TestTenantPluginCreateMapDestFailsClosed(t *testing.T) {
	db := newPluginDB(t)

	ctx := tenant.WithTenantID(context.Background(), 3)
	err := db.WithContext(ctx).Model(&tenantRow{}).Create(map[string]any{
		"id": 21, "tenant_id": 3, "name": "map-dest",
	}).Error
	if err == nil {
		t.Fatal("map dest：期望报错，实际写入成功")
	}
	if !strings.Contains(err.Error(), "map") || !strings.Contains(err.Error(), "tenant.Skip") {
		t.Fatalf("错误应说明 map dest 无法回填并提示 tenant.Skip，实际 %v", err)
	}
	if got := countRows(t, db); got != 3 {
		t.Fatalf("失败的写入不应落库，期望 3 行，实际 %d 行", got)
	}
}
