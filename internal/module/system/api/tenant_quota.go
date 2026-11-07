package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/quota"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// GetCurrentTenantQuota 当前租户配额（GET /system/tenant/quota，登录即可，
// spec §5.1）：租户查看自己的套餐限额与用量。
func (h *Handler) GetCurrentTenantQuota(c *gin.Context) {
	h.serveTenantQuota(c, tenant.ID(c.Request.Context()))
}

// GetTenantQuota 指定租户配额（GET /system/tenant/:id/quota，spec §5.1）：
// 平台租户可查任意租户；非平台租户只能查本租户，越权一律 404——配额是
// 租户私有数据，跨租户不可见（spec §4.11.4 的 404 语义，不走平台 403 组）。
func (h *Handler) GetTenantQuota(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	current := tenant.ID(c.Request.Context())
	if current != PlatformTenantID && id != current {
		response.Fail(c, xerr.NotFound("租户不存在"))
		return
	}
	h.serveTenantQuota(c, id)
}

// serveTenantQuota 查询配额并把 service 的中性 Status 转成 API 响应 DTO。
func (h *Handler) serveTenantQuota(c *gin.Context, tenantID int64) {
	status, err := h.svc.TenantQuota(c.Request.Context(), tenantID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tenantQuotaResp(status))
}

// tenantQuotaResp 把中性配额状态映射为 limit/used/unlimited 响应结构。
func tenantQuotaResp(status quota.Status) dto.TenantQuotaResp {
	return dto.TenantQuotaResp{
		Unlimited: status.Limits.Unlimited,
		Limit: dto.TenantQuotaLimits{
			MaxUsers:       status.Limits.MaxUsers,
			MaxStorageMB:   status.Limits.MaxStorageMB,
			MaxSMSMonthly:  status.Limits.MaxSMSMonthly,
			MaxMailMonthly: status.Limits.MaxMailMonthly,
		},
		Used: dto.TenantQuotaUsage{
			Users:        status.Usage.Users,
			StorageBytes: status.Usage.StorageBytes,
			SMSMonthly:   status.Usage.SMSMonthly,
			MailMonthly:  status.Usage.MailMonthly,
		},
	}
}
