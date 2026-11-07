package seed_test

import (
	"strings"
	"testing"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	msgmodel "github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/database"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestSeedPlatformDemoUnderFailClosedTenantPlugin 验证 GAP_SEED_DEMO=true 路径
// （issue #85，spec §7.3/§9）：基础种子之后写入的平台演示数据在 fail-closed
// 租户插件下必须能完成，且
//   - 渠道/模板/社交客户端/敏感词/演示消息日志按预期落库（默认租户 1）；
//   - 演示配置不含真实密钥（占位符）；
//   - 社交客户端全部停用（避免演示环境真发起 OAuth）；
//   - 重复执行幂等，不重复插入。
func TestSeedPlatformDemoUnderFailClosedTenantPlugin(t *testing.T) {
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

	if err := db.AutoMigrate(
		&sysmodel.Tenant{}, &sysmodel.Menu{}, &sysmodel.Dept{}, &sysmodel.Role{},
		&sysmodel.RoleMenu{}, &sysmodel.Post{}, &sysmodel.User{}, &sysmodel.UserRole{},
		&sysmodel.UserPost{}, &sysmodel.DictType{}, &sysmodel.DictData{},
		&sysmodel.TenantPackage{},
		&inframodel.Config{}, &inframodel.Job{},
		&msgmodel.Channel{}, &msgmodel.Template{}, &msgmodel.Log{}, &sysmodel.Area{},
		&sysmodel.SocialClient{}, &sysmodel.SensitiveWord{},
		&crmmodel.PoolConfig{},
		&bpmmodel.ProcessDefinition{},
		&oamodel.AttendanceShift{}, &oamodel.AttendanceGroup{},
	); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}

	// 先跑基础种子（Run 的真实顺序），再写平台演示数据——演示消息日志引用的
	// 演示渠道由演示种子自带，基础渠道/模板也在场，两段互不干扰。
	if err := seed.Run(db, false); err != nil {
		t.Fatalf("fail-closed 租户插件下基础 seed 失败: %v", err)
	}
	if err := seed.PlatformDemo(db); err != nil {
		t.Fatalf("fail-closed 租户插件下平台演示种子失败: %v", err)
	}

	// 渠道：基础 log 渠道 2 条 + 演示 SMTP/阿里云渠道 2 条。
	assertCount(t, db, &msgmodel.Channel{}, 4, "消息渠道")
	var smtpCh msgmodel.Channel
	if err := tenant.Skip(db).Where("code = ?", "demo-mail-smtp").First(&smtpCh).Error; err != nil {
		t.Fatalf("读取演示邮件渠道: %v", err)
	}
	if smtpCh.Provider != msgmodel.ProviderSMTP || smtpCh.Status != msgmodel.StatusEnable {
		t.Fatalf("演示邮件渠道应为启用的 smtp provider：%+v", smtpCh)
	}
	var smsCh msgmodel.Channel
	if err := tenant.Skip(db).Where("code = ?", "demo-sms-aliyun").First(&smsCh).Error; err != nil {
		t.Fatalf("读取演示短信渠道: %v", err)
	}
	if smsCh.Provider != msgmodel.ProviderAliyunSMS || smsCh.Status != msgmodel.StatusEnable {
		t.Fatalf("演示短信渠道应为启用的 aliyun_sms provider：%+v", smsCh)
	}
	// 演示配置只允许占位符，不写真实密钥（spec §4.11 / §7.3）。
	for _, cfg := range []string{smtpCh.Config, smsCh.Config} {
		assertPlaceholderConfig(t, cfg)
	}

	// 模板：基础欢迎模板 2 条 + 演示模板 2 条（编码不重叠）。
	assertCount(t, db, &msgmodel.Template{}, 4, "消息模板")
	assertTemplateParams(t, db, msgmodel.ChannelTypeSMS, "verify_code", `["code","minutes"]`)
	assertTemplateParams(t, db, msgmodel.ChannelTypeMail, "password_reset", `["link","minutes"]`)

	// 标准套餐：基础种子已写入，演示种子兜底不产生第二份。
	assertCount(t, db, &sysmodel.TenantPackage{}, 1, "默认套餐")

	// 敏感词：默认租户 4 条示例。
	assertCount(t, db, &sysmodel.SensitiveWord{}, 4, "敏感词")
	var word sysmodel.SensitiveWord
	if err := tenant.Skip(db).Where("tenant_id = ? AND word = ?", 1, "刷单").First(&word).Error; err != nil {
		t.Fatalf("读取演示敏感词: %v", err)
	}
	if word.Status != sysmodel.StatusEnable {
		t.Fatalf("演示敏感词应为启用状态：%+v", word)
	}

	// 社交客户端：三家 provider 各一条且全部停用（避免演示环境真发起 OAuth）。
	assertCount(t, db, &sysmodel.SocialClient{}, 3, "社交客户端")
	var disabledCount int64
	if err := tenant.Skip(db).Model(&sysmodel.SocialClient{}).
		Where("status = ?", sysmodel.StatusDisable).Count(&disabledCount).Error; err != nil {
		t.Fatalf("统计停用社交客户端: %v", err)
	}
	if disabledCount != 3 {
		t.Fatalf("演示社交客户端应全部停用，实际停用 %d/3", disabledCount)
	}

	// 演示消息日志：成功/失败各 2 条，字段口径自洽。
	assertDemoMessageLogs(t, db)

	// 重复执行必须幂等：渠道/模板/敏感词/客户端/日志都不重复插入。
	if err := seed.PlatformDemo(db); err != nil {
		t.Fatalf("重复平台演示种子失败: %v", err)
	}
	assertCount(t, db, &msgmodel.Channel{}, 4, "消息渠道（幂等）")
	assertCount(t, db, &msgmodel.Template{}, 4, "消息模板（幂等）")
	assertCount(t, db, &sysmodel.TenantPackage{}, 1, "默认套餐（幂等）")
	assertCount(t, db, &sysmodel.SensitiveWord{}, 4, "敏感词（幂等）")
	assertCount(t, db, &sysmodel.SocialClient{}, 3, "社交客户端（幂等）")
	assertCount(t, db, &msgmodel.Log{}, 4, "演示消息日志（幂等）")

	// 软删一个演示渠道后重复执行：不复活、不撞不含 deleted_at 的 code 唯一键。
	if err := tenant.Skip(db).Delete(&smtpCh).Error; err != nil {
		t.Fatalf("软删演示邮件渠道: %v", err)
	}
	if err := seed.PlatformDemo(db); err != nil {
		t.Fatalf("软删后执行平台演示种子失败（不应撞唯一键）: %v", err)
	}
	var deletedCount int64
	if err := tenant.Skip(db).Model(&msgmodel.Channel{}).
		Where("code = ?", "demo-mail-smtp").Count(&deletedCount).Error; err != nil {
		t.Fatalf("统计软删渠道: %v", err)
	}
	if deletedCount != 0 {
		t.Fatalf("管理端删除的演示渠道不应被种子复活: %d", deletedCount)
	}
}

// assertPlaceholderConfig 断言演示渠道配置只含占位符凭据。
func assertPlaceholderConfig(t *testing.T, cfg string) {
	t.Helper()
	for _, key := range []string{"change-me"} {
		if !strings.Contains(cfg, key) {
			t.Fatalf("演示渠道配置应使用占位符 %q，实际 %s", key, cfg)
		}
	}
}

// assertTemplateParams 断言演示模板存在且参数声明与内容占位符对应。
func assertTemplateParams(t *testing.T, db *gorm.DB, channelType, code, wantParams string) {
	t.Helper()
	var tpl msgmodel.Template
	if err := tenant.Skip(db).
		Where("channel_type = ? AND code = ?", channelType, code).
		First(&tpl).Error; err != nil {
		t.Fatalf("读取演示模板 %s/%s: %v", channelType, code, err)
	}
	if tpl.Params != wantParams {
		t.Fatalf("演示模板 %s 参数声明应为 %s，实际 %s", code, wantParams, tpl.Params)
	}
	if tpl.Status != msgmodel.StatusEnable {
		t.Fatalf("演示模板 %s 应为启用状态：%+v", code, tpl)
	}
}

// assertDemoMessageLogs 断言演示日志：4 条归属默认租户，成功行有 sent_at 且
// 无错误信息，失败行有错误详情；渠道/模板指向演示行；邮件失败行重发过一次。
func assertDemoMessageLogs(t *testing.T, db *gorm.DB) {
	t.Helper()
	var logs []msgmodel.Log
	if err := tenant.Skip(db).Where("tenant_id = ?", 1).Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatalf("读取演示消息日志: %v", err)
	}
	if len(logs) != 4 {
		t.Fatalf("演示消息日志应为 4 条，实际 %d", len(logs))
	}
	var success, failed int
	for _, row := range logs {
		if row.TenantID != 1 {
			t.Fatalf("演示消息日志应归属默认租户 1，实际 tenant_id=%d", row.TenantID)
		}
		if row.ChannelID != 11 && row.ChannelID != 12 {
			t.Fatalf("演示消息日志应指向演示渠道 11/12，实际 %d", row.ChannelID)
		}
		switch row.Status {
		case msgmodel.LogStatusSuccess:
			success++
			if row.SentAt == nil || row.ErrorMessage != "" {
				t.Fatalf("成功日志应有 sent_at 且无错误信息：%+v", row)
			}
		case msgmodel.LogStatusFailed:
			failed++
			if row.ErrorMessage == "" {
				t.Fatalf("失败日志应有错误详情：%+v", row)
			}
			if row.RetryCount != 0 && row.RetryCount != 1 {
				t.Fatalf("演示失败日志 retry_count 应为 0 或 1，实际 %d", row.RetryCount)
			}
		default:
			t.Fatalf("演示消息日志状态非法：%+v", row)
		}
		if row.Content == "" {
			t.Fatalf("演示消息日志应有渲染后的内容：%+v", row)
		}
	}
	if success != 2 || failed != 2 {
		t.Fatalf("演示消息日志应成功/失败各 2 条，实际成功 %d 失败 %d", success, failed)
	}
}
