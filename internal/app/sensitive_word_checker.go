package app

import (
	"context"

	crmservice "github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
)

// sensitiveWordChecker 把 system 的敏感词服务适配为 OA/CRM 各自定义的
// SensitiveWordChecker 窄接口（issue #81，spec §4.7.4/§4.7.5，注入模式与
// quotaGateway 同款）：业务模块只依赖自己的接口声明，不 import system。
// 检查在业务事务外、落库前进行——命中 400「内容包含敏感词：…」时调用方
// 不写任何数据。
type sensitiveWordChecker struct {
	svc *systemservice.SensitiveWordService
}

// CheckSensitiveWords 实现 OA/CRM 两侧窄接口（方法签名一致，一个类型同时满足）。
func (c sensitiveWordChecker) CheckSensitiveWords(ctx context.Context, texts ...string) error {
	return c.svc.CheckSensitiveWords(ctx, texts...)
}

var (
	_ oaservice.SensitiveWordChecker  = sensitiveWordChecker{}
	_ crmservice.SensitiveWordChecker = sensitiveWordChecker{}
)
