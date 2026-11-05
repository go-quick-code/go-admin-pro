package service

import (
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"github.com/mojocn/base64Captcha"
)

var captchaStore = base64Captcha.DefaultMemStore

// CaptchaEnabled 是否启用验证码。
func (s *Service) CaptchaEnabled() bool {
	return s.cfg.Security.Captcha.Enabled
}

// GenerateCaptcha 生成验证码。
func (s *Service) GenerateCaptcha() (*dto.CaptchaResp, error) {
	if !s.cfg.Security.Captcha.Enabled {
		return &dto.CaptchaResp{Enabled: false}, nil
	}
	driver := base64Captcha.NewDriverString(
		s.cfg.Security.Captcha.Height,
		s.cfg.Security.Captcha.Width,
		2,
		base64Captcha.OptionShowSlimeLine|base64Captcha.OptionShowHollowLine,
		s.cfg.Security.Captcha.Length,
		"23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ",
		nil,
		nil,
		nil,
	)
	c := base64Captcha.NewCaptcha(driver, captchaStore)
	id, b64s, _, err := c.Generate()
	if err != nil {
		return nil, xerr.Internal(err)
	}
	return &dto.CaptchaResp{Enabled: true, Key: id, Image: b64s}, nil
}

// VerifyCaptcha 校验验证码，校验后立即失效。
func (s *Service) VerifyCaptcha(key, answer string) bool {
	if !s.cfg.Security.Captcha.Enabled {
		return true
	}
	key = strings.TrimSpace(key)
	answer = strings.TrimSpace(answer)
	if key == "" || answer == "" {
		return false
	}
	return captchaStore.Verify(key, answer, true)
}
