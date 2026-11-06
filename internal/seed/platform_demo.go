// platform_demo.go 写入平台能力演示数据（issue #85，spec §7.3/§9）：
//   - 标准租户套餐：兜底保证默认「标准版」存在（基础种子在套餐表为空时已写
//     入，这里复用同一幂等逻辑，不产生第二份）；
//   - 消息渠道：SMTP 邮件与阿里云短信各一条，配置全部是占位符（不写真实密钥，
//     spec §4.11），发送前需在「消息管理」里改成真实配置；
//   - 消息模板：短信验证码与邮件重置密码各一条（编码不与基础种子的 welcome
//     重叠）；
//   - 敏感词示例：默认租户几条常用词，按词判重（唯一键含 deleted_at，软删后
//     重新启动会补回，符合该表的重新添加语义）；
//   - 社交客户端：三家 provider 各一条且全部停用（status=1），避免演示环境
//     真发起 OAuth 跳转；使用前需改为真实 AppID/Secret 并启用；
//   - 演示消息日志：默认租户 4 条成功/失败记录，字段口径与发送链路落库一致
//     （成功行有 sent_at 且无错误信息，失败行带 provider 风格的错误详情；
//     邮件失败行 retry_count=1 表示重发过一次仍失败，spec §4.3.7）。
//
// 幂等：渠道/模板/社交客户端按编码判重——这些表的 code 上有不含 deleted_at
// 的唯一键（迁移 00024/00025），任何状态下已存在即跳过，管理端删掉的演示行
// 不复活也不会撞唯一键；message_log 是 append-only 表，仅在表为空时整段写入。
// 不依赖 CRM/OA 演示数据：init 注册进 seed.Run 的 withDemo 分支（顺序无关），
// 集成测试与选择性补写可直接调用 PlatformDemo。
package seed

import (
	"fmt"
	"log/slog"
	"time"

	msgmodel "github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// platformDemoTenantID 是平台演示数据的归属租户（默认租户 1）。不复用
// crm_demo.go 的 demoTenantID：代码生成器的编译校验副本会剥离 crm_demo.go，
// 平台演示文件必须自含其依赖（见 seed.go 的 seedCRMDemoFunc 注释）。
const platformDemoTenantID int64 = 1

// 演示行固定 ID 段：11 起（基础种子的渠道/模板占 1–2，与 crm_demo 的 1001 段
// 互不重叠）。演示消息日志的 channel_id 引用这里写入的渠道 ID。
const (
	demoSMTPChannelID   int64 = 11
	demoAliyunChannelID int64 = 12
	demoSMSTemplateID   int64 = 11
	demoMailTemplateID  int64 = 12
)

// init 把平台演示数据入口注册给 seed.Run（见 seed.go 的 seedCRMDemoFunc 注释）。
func init() { seedPlatformDemoFunc = seedPlatformDemo }

// PlatformDemo 写入平台能力演示数据（渠道/模板/套餐/敏感词/社交客户端/消息
// 日志），幂等可重复执行。seed.Run 在 GAP_SEED_DEMO=true 时经 withDemo 分支
// 调用；只想补写平台演示数据（不带 CRM/OA 演示组织）时也可直接调用。
func PlatformDemo(db *gorm.DB) error { return seedPlatformDemo(db) }

func seedPlatformDemo(db *gorm.DB) error {
	if err := ensureStandardTenantPackage(db); err != nil {
		return err
	}
	if err := seedDemoMessageChannels(db); err != nil {
		return err
	}
	if err := seedDemoMessageTemplates(db); err != nil {
		return err
	}
	if err := seedDemoSensitiveWords(db); err != nil {
		return err
	}
	if err := seedDemoSocialClients(db); err != nil {
		return err
	}
	return seedDemoMessageLogs(db)
}

// createDemoIfAbsent 按 query 判重（Unscoped，含软删除行）写入演示行：
// 已存在（任何删除状态）即跳过。用于 code 上有不含 deleted_at 唯一键的表，
// 避免管理端删除演示行后重复启动撞唯一键。
func createDemoIfAbsent(db *gorm.DB, label string, model any, query string, args []any, row any) error {
	var count int64
	if err := tenant.Skip(db).Unscoped().Model(model).Where(query, args...).Count(&count).Error; err != nil {
		return fmt.Errorf("统计演示%s: %w", label, err)
	}
	if count > 0 {
		return nil
	}
	if err := tenant.Skip(db).Create(row).Error; err != nil {
		return fmt.Errorf("写入演示%s: %w", label, err)
	}
	return nil
}

// seedDemoMessageChannels 写入 SMTP 邮件与阿里云短信演示渠道。配置是占位符，
// 不含真实密钥（spec §4.11：DB 明文 + API 脱敏的前提是库里本来就不放真实凭据，
// 演示种子尤其如此）；渠道启用，发送失败会按 provider 错误落失败日志。
func seedDemoMessageChannels(db *gorm.DB) error {
	channels := []msgmodel.Channel{
		{
			BaseEntity:  msgmodel.BaseEntity{ID: demoSMTPChannelID},
			ChannelType: msgmodel.ChannelTypeMail,
			Code:        "demo-mail-smtp",
			Name:        "演示邮件渠道（SMTP）",
			Provider:    msgmodel.ProviderSMTP,
			Config:      `{"host":"smtp.example.com","port":587,"username":"noreply@example.com","password":"change-me","from":"noreply@example.com","fromName":"go-admin-pro 演示","tls":"starttls"}`,
			Status:      msgmodel.StatusEnable,
			Remark:      "演示渠道：host/username/password 均为占位符，发送前请在管理端改为真实配置",
		},
		{
			BaseEntity:  msgmodel.BaseEntity{ID: demoAliyunChannelID},
			ChannelType: msgmodel.ChannelTypeSMS,
			Code:        "demo-sms-aliyun",
			Name:        "演示短信渠道（阿里云）",
			Provider:    msgmodel.ProviderAliyunSMS,
			Config:      `{"accessKeyId":"change-me","accessKeySecret":"change-me","signName":"演示签名","regionId":"cn-hangzhou"}`,
			Status:      msgmodel.StatusEnable,
			Remark:      "演示渠道：AK/SK 与签名均为占位符，发送前请在管理端改为真实配置",
		},
	}
	for i := range channels {
		c := channels[i]
		if err := createDemoIfAbsent(db, "消息渠道 "+c.Code, &msgmodel.Channel{},
			"code = ?", []any{c.Code}, &c); err != nil {
			return err
		}
	}
	return nil
}

// seedDemoMessageTemplates 写入短信验证码与邮件重置密码演示模板，
// 参数声明与内容占位符一一对应（spec §4.3.3：缺失/多余参数发送 400）。
func seedDemoMessageTemplates(db *gorm.DB) error {
	templates := []msgmodel.Template{
		{
			BaseEntity:  msgmodel.BaseEntity{ID: demoSMSTemplateID},
			ChannelType: msgmodel.ChannelTypeSMS,
			Code:        "verify_code",
			Name:        "验证码短信",
			Content:     "您的验证码是 {{code}}，{{minutes}} 分钟内有效。",
			Params:      `["code","minutes"]`,
			Status:      msgmodel.StatusEnable,
			Remark:      "演示模板：code 直接对应阿里云控制台模板编码的使用约定（ADR 0010）",
		},
		{
			BaseEntity:  msgmodel.BaseEntity{ID: demoMailTemplateID},
			ChannelType: msgmodel.ChannelTypeMail,
			Code:        "password_reset",
			Name:        "重置密码邮件",
			Title:       "重置密码通知",
			Content:     "<p>您好，请点击链接重置密码：<a href=\"{{link}}\">{{link}}</a>，{{minutes}} 分钟内有效。</p>",
			Params:      `["link","minutes"]`,
			Status:      msgmodel.StatusEnable,
			Remark:      "演示模板：配合演示邮件渠道使用",
		},
	}
	for i := range templates {
		t := templates[i]
		if err := createDemoIfAbsent(db, "消息模板 "+t.Code, &msgmodel.Template{},
			"channel_type = ? AND code = ?", []any{t.ChannelType, t.Code}, &t); err != nil {
			return err
		}
	}
	return nil
}

// seedDemoSensitiveWords 写入默认租户的敏感词示例，按词判重（FirstOrCreate，
// 默认 scope 排除软删除：软删词的唯一键含 deleted_at，补回合法，见迁移 00027）。
func seedDemoSensitiveWords(db *gorm.DB) error {
	words := []sysmodel.SensitiveWord{
		{Word: "刷单", Category: "欺诈", Remark: "演示词库：命中后公告/跟进落库前被拒绝（spec §4.7.4）"},
		{Word: "代开发票", Category: "欺诈", Remark: "演示词库：命中后公告/跟进落库前被拒绝（spec §4.7.4）"},
		{Word: "加微信", Category: "广告", Remark: "演示词库：命中后公告/跟进落库前被拒绝（spec §4.7.4）"},
		{Word: "内部资料", Category: "泄密", Remark: "演示词库：命中后公告/跟进落库前被拒绝（spec §4.7.4）"},
	}
	for i := range words {
		w := words[i]
		w.TenantID = platformDemoTenantID
		w.Status = sysmodel.StatusEnable
		if err := tenant.Skip(db).
			Where("tenant_id = ? AND word = ?", platformDemoTenantID, w.Word).
			FirstOrCreate(&w).Error; err != nil {
			return fmt.Errorf("写入演示敏感词 %s: %w", w.Word, err)
		}
	}
	return nil
}

// seedDemoSocialClients 写入三家 provider 的演示社交客户端，全部停用：
// 停用客户端不参与 authorize-url 换取，演示环境不会真发起 OAuth 跳转
// （spec §7.3）；client_id/client_secret 是占位符，使用前先改配置再启用。
// 自定义端点留空，走 provider 内置默认值（spec §4.4.8 / #77）。
func seedDemoSocialClients(db *gorm.DB) error {
	clients := []sysmodel.SocialClient{
		{
			Code: "demo-wechat-open", Name: "演示微信开放平台", Provider: "wechat_open",
			ClientID: "wx-change-me", ClientSecret: "change-me",
			RedirectURI: "http://localhost:5173/social/callback",
			Status:      sysmodel.StatusDisable,
			Remark:      "演示客户端：默认停用，避免演示环境真发起 OAuth；改为真实 AppID/AppSecret 并启用后生效",
		},
		{
			Code: "demo-dingtalk", Name: "演示钉钉", Provider: "dingtalk",
			ClientID: "ding-change-me", ClientSecret: "change-me",
			RedirectURI: "http://localhost:5173/social/callback",
			Status:      sysmodel.StatusDisable,
			Remark:      "演示客户端：默认停用，避免演示环境真发起 OAuth；改为真实 ClientID/Secret 并启用后生效",
		},
		{
			Code: "demo-wecom", Name: "演示企业微信", Provider: "wecom",
			ClientID: "ww-change-me", ClientSecret: "change-me",
			RedirectURI: "http://localhost:5173/social/callback",
			Status:      sysmodel.StatusDisable,
			Remark:      "演示客户端：默认停用，避免演示环境真发起 OAuth；改为真实 CorpID/Secret 并启用后生效",
		},
	}
	for i := range clients {
		c := clients[i]
		if err := createDemoIfAbsent(db, "社交客户端 "+c.Code, &sysmodel.SocialClient{},
			"code = ?", []any{c.Code}, &c); err != nil {
			return err
		}
	}
	return nil
}

// seedDemoMessageLogs 写入默认租户的演示消息日志：成功/失败各两条，字段与
// 发送链路（service.deliver → recordSendResult）的落库口径一致——渠道 ID 指向
// 演示渠道、模板编码指向演示模板、内容按模板渲染结果填写、错误详情用 provider
// 的报错文案风格。message_log 是 append-only 表（无自然键可判重），仅当表为空
// 时整段写入，已有日志（含真实发送记录）不掺演示行。
func seedDemoMessageLogs(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&msgmodel.Log{}).Count(&count).Error; err != nil {
		return fmt.Errorf("统计 message_log: %w", err)
	}
	if count > 0 {
		return nil
	}

	now := time.Now()
	mailFailAt := now.Add(-15 * time.Minute)
	smsFailAt := now.Add(-30 * time.Minute)
	smsOKAt := now.Add(-90 * time.Minute)
	mailOKAt := now.Add(-2 * time.Hour)
	rows := []msgmodel.Log{
		{
			// 邮件成功：message_id 是 SMTPSender 的 <smtp-纳秒@host> 格式。
			CreatedAt: mailOKAt, TenantID: platformDemoTenantID,
			ChannelType: msgmodel.ChannelTypeMail, ChannelID: demoSMTPChannelID,
			TemplateCode:      "password_reset",
			Recipient:         "zhang@example.com",
			Title:             "重置密码通知",
			Content:           "<p>您好，请点击链接重置密码：<a href=\"https://demo.example.com/reset-password?token=demo01\">https://demo.example.com/reset-password?token=demo01</a>，10 分钟内有效。</p>",
			Status:            msgmodel.LogStatusSuccess,
			ProviderMessageID: fmt.Sprintf("<smtp-%d@smtp.example.com>", mailOKAt.UnixNano()),
			RetryCount:        0,
			SentAt:            &mailOKAt,
		},
		{
			// 短信成功：message_id 对应阿里云 BizID（数字串）。
			CreatedAt: smsOKAt, TenantID: platformDemoTenantID,
			ChannelType: msgmodel.ChannelTypeSMS, ChannelID: demoAliyunChannelID,
			TemplateCode:      "verify_code",
			Recipient:         "13800001111",
			Content:           "您的验证码是 246810，5 分钟内有效。",
			Status:            msgmodel.LogStatusSuccess,
			ProviderMessageID: "9071064362190546",
			RetryCount:        0,
			SentAt:            &smsOKAt,
		},
		{
			// 短信失败：错误详情是 aliyun.go 的报错文案风格（落日志不回显调用方）。
			CreatedAt: smsFailAt, TenantID: platformDemoTenantID,
			ChannelType: msgmodel.ChannelTypeSMS, ChannelID: demoAliyunChannelID,
			TemplateCode: "verify_code",
			Recipient:    "13800002222",
			Content:      "您的验证码是 135790，5 分钟内有效。",
			Status:       msgmodel.LogStatusFailed,
			ErrorMessage: "阿里云短信发送失败：错误码 isv.BUSINESS_LIMIT_CONTROL：触发分钟级流控",
			RetryCount:   0,
			SentAt:       &smsFailAt,
		},
		{
			// 邮件失败：重发过一次仍失败（retry_count=1，spec §4.3.7 重发更新原行），
			// 错误详情是 smtp.go 的「步骤前缀 + 底因」文案风格。
			CreatedAt: mailFailAt, TenantID: platformDemoTenantID,
			ChannelType: msgmodel.ChannelTypeMail, ChannelID: demoSMTPChannelID,
			TemplateCode: "password_reset",
			Recipient:    "li@example.com",
			Title:        "重置密码通知",
			Content:      "<p>您好，请点击链接重置密码：<a href=\"https://demo.example.com/reset-password?token=demo02\">https://demo.example.com/reset-password?token=demo02</a>，10 分钟内有效。</p>",
			Status:       msgmodel.LogStatusFailed,
			ErrorMessage: "SMTP 认证失败：535 5.7.8 Username and Password not accepted（请检查渠道 username/password 配置）",
			RetryCount:   1,
			SentAt:       &mailFailAt,
		},
	}
	if err := tenant.Skip(db).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示消息日志: %w", err)
	}
	slog.Info("seed platform demo created",
		"channels", 2, "templates", 2, "sensitiveWords", 4,
		"socialClients", 3, "messageLogs", len(rows))
	return nil
}
