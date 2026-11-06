// sensitive_word.go 是 OA 侧的敏感词检查接缝（issue #81，spec §4.7.4）：
// 公告创建/修改在落库前检查标题与正文，命中 400「内容包含敏感词：…」且不落库。
// OA 不反向依赖 system 模块，检查实现由 internal/app 装配 system 的
// SensitiveWordService 适配器注入；未注入（nil）时跳过检查——测试基座可
// 显式关闭，业务单测无需携带词库依赖。
package service

import "context"

// SensitiveWordChecker 是敏感词检查的窄接口（由 internal/app 注入实现）。
type SensitiveWordChecker interface {
	// CheckSensitiveWords 依次检测多段文本；任一命中返回 400 业务错误
	//「内容包含敏感词：a、b」（词去重、按首次出现位置排序）。
	CheckSensitiveWords(ctx context.Context, texts ...string) error
}

// SetSensitiveWordChecker 注入敏感词检查实现（app 装配处调用）。
func (s *Service) SetSensitiveWordChecker(checker SensitiveWordChecker) {
	s.sensitive = checker
}

// checkSensitiveWords 统一入口：未注入检查器时跳过（spec §4.7.5）。
func (s *Service) checkSensitiveWords(ctx context.Context, texts ...string) error {
	if s.sensitive == nil {
		return nil
	}
	return s.sensitive.CheckSensitiveWords(ctx, texts...)
}
