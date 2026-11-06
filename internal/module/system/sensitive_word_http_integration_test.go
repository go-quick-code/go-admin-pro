//go:build integration

// 敏感词 HTTP 集成测试（issue #81，docs/platform-mvp-spec.md §3.8/§4.7/§5.6）：
// 走 #3 基座的真实 router + 生产中间件（JWT/权限/租户插件）+ 真实 MySQL。
// 覆盖：词库 CRUD 与字段校验、唯一 409、单租户上限 5000、检测端点（命中/
// 打码/大小写/去重）、CRUD 缓存失效（停用/删除即不命中）、跨租户词库隔离
// （404）、OA 公告与 CRM 跟进的落库前拦截（命中 400 不落库、未命中正常）。
package system_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createSensitiveWordAPI 调创建端点，返回 (HTTP 状态, 业务 msg)。
func createSensitiveWordAPI(t *testing.T, env *testutil.IntegrationEnv, token, word string, extra map[string]any) (int, string) {
	t.Helper()
	payload := map[string]any{"word": word, "status": 0}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/system/sensitive-word", token, payload)
	return status, api.Msg
}

// mustCreateSensitiveWord 创建敏感词并断言成功。
func mustCreateSensitiveWord(t *testing.T, env *testutil.IntegrationEnv, token, word string) {
	t.Helper()
	status, msg := createSensitiveWordAPI(t, env, token, word, nil)
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建敏感词 %q：HTTP %d msg %q，期望成功", word, status, msg)
	}
}

// checkTextAPI 调检测端点，返回 (HTTP 状态, 命中词, 打码文本, 业务 msg)。
func checkTextAPI(t *testing.T, env *testutil.IntegrationEnv, token, text string) (int, []string, string, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/sensitive-word/check", token, map[string]any{"text": text})
	if status != http.StatusOK || api.Code != 0 {
		return status, nil, "", api.Msg
	}
	var data struct {
		Words  []string `json:"words"`
		Masked string   `json:"masked"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析检测结果: %v（data=%s）", err, string(api.Data))
	}
	return status, data.Words, data.Masked, ""
}

// sensitiveWordPageTotal 查询词库分页 total（默认过滤条件）。
func sensitiveWordPageTotal(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int64, []map[string]any) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/sensitive-word/page?pageNo=1&pageSize=100"+query, token, nil)
	mustOK(t, "敏感词分页", status, api)
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析敏感词分页: %v（data=%s）", err, string(api.Data))
	}
	return page.Total, page.List
}

// TestIntegrationSensitiveWordCRUD 词库 CRUD 与校验：创建/分页/修改/删除、
// 去空白与 1–50 字符边界、租户内唯一 409、状态校验。
func TestIntegrationSensitiveWordCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	mustCreateSensitiveWord(t, env, admin, "违禁品")
	mustCreateSensitiveWord(t, env, admin, "  前后空白  ") // 去空白后为「前后空白」

	// 分页回读：word 已去首尾空白。
	total, list := sensitiveWordPageTotal(t, env, admin, "")
	if total != 2 {
		t.Fatalf("词库应 2 条，实际 %d", total)
	}
	words := map[string]bool{}
	for _, row := range list {
		words[row["word"].(string)] = true
	}
	if !words["违禁品"] || !words["前后空白"] {
		t.Fatalf("词库应含去空白后的词条，实际 %v", list)
	}

	// 唯一：同词（含仅空白差异）409。
	if status, msg := createSensitiveWordAPI(t, env, admin, "违禁品", nil); status != http.StatusConflict {
		t.Fatalf("重复词条应 409，实际 HTTP %d msg %q", status, msg)
	}
	if status, _ := createSensitiveWordAPI(t, env, admin, "  违禁品 ", nil); status != http.StatusConflict {
		t.Fatalf("去空白后同词应 409，实际 HTTP %d", status)
	}

	// 边界：空词 / 全空白 400；51 字符 400；50 字符成功。
	if status, _ := createSensitiveWordAPI(t, env, admin, "   ", nil); status != http.StatusBadRequest {
		t.Fatalf("全空白词应 400，实际 HTTP %d", status)
	}
	long51 := strings.Repeat("长", 51)
	if status, _ := createSensitiveWordAPI(t, env, admin, long51, nil); status != http.StatusBadRequest {
		t.Fatalf("51 字符应 400，实际 HTTP %d", status)
	}
	long50 := strings.Repeat("词", 50)
	mustCreateSensitiveWord(t, env, admin, long50)

	// 修改：改词 + 停用；分页回读生效。
	var target map[string]any
	for _, row := range list {
		if row["word"] == "违禁品" {
			target = row
		}
	}
	if target == nil {
		t.Fatalf("分页应含违禁品，实际 %v", list)
	}
	targetID := int64(target["id"].(float64))
	status, api := env.Request(t, http.MethodPut, "/system/sensitive-word", admin, map[string]any{
		"id": targetID, "word": "违禁品改", "category": "测试", "status": 1,
	})
	mustOK(t, "修改敏感词", status, api)
	_, list2 := sensitiveWordPageTotal(t, env, admin, "&word=违禁品改")
	if len(list2) != 1 || list2[0]["status"].(float64) != 1 {
		t.Fatalf("修改后应查到停用的「违禁品改」，实际 %v", list2)
	}

	// 删除：软删后分页不可见，同词可重新添加（deleted_at 入唯一键）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/sensitive-word/%d", targetID), admin, nil)
	mustOK(t, "删除敏感词", status, api)
	total, _ = sensitiveWordPageTotal(t, env, admin, "")
	if total != 2 {
		t.Fatalf("删除后词库应 2 条（前后空白/50字词），实际 %d", total)
	}
	mustCreateSensitiveWord(t, env, admin, "违禁品改")
}

// TestIntegrationSensitiveWordCheck 检测端点：命中去重/打码/ASCII 大小写
// 折叠/无命中。
func TestIntegrationSensitiveWordCheck(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	mustCreateSensitiveWord(t, env, admin, "违禁词")
	mustCreateSensitiveWord(t, env, admin, "bad")

	// 命中：去重（同词两次只报一次）、打码、按首现位置排序。
	status, words, masked, _ := checkTextAPI(t, env, admin, "一条违禁词记录，又是违禁词，really BAD!")
	if status != http.StatusOK {
		t.Fatalf("检测端点应 200，实际 HTTP %d", status)
	}
	if len(words) != 2 || words[0] != "违禁词" || words[1] != "bad" {
		t.Fatalf("命中词 = %v，期望 [违禁词 bad]", words)
	}
	if masked != "一条***记录，又是***，really ***!" {
		t.Fatalf("打码文本 = %q", masked)
	}

	// 无命中：词表为空、文本原样。
	_, words, masked, _ = checkTextAPI(t, env, admin, "正常内容")
	if len(words) != 0 || masked != "正常内容" {
		t.Fatalf("无命中应返回空词表与原文，实际 %v %q", words, masked)
	}
}

// TestIntegrationSensitiveWordCacheInvalidation CRUD 后缓存失效（spec §4.7.3）：
// 新增/停用/启用/删除立即影响检测，无需重启或手动清缓存。
func TestIntegrationSensitiveWordCacheInvalidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 先触发一次检测，把空缓存建立起来，再验证 CRUD 能打破它。
	if _, words, _, _ := checkTextAPI(t, env, admin, "缓存词"); len(words) != 0 {
		t.Fatalf("初始不应命中，实际 %v", words)
	}

	mustCreateSensitiveWord(t, env, admin, "缓存词")
	if _, words, _, _ := checkTextAPI(t, env, admin, "含缓存词文本"); len(words) != 1 || words[0] != "缓存词" {
		t.Fatalf("创建后应立即命中，实际 %v", words)
	}

	// 停用词不参与匹配：status=1 后不命中。
	_, list := sensitiveWordPageTotal(t, env, admin, "&word=缓存词")
	rowID := int64(list[0]["id"].(float64))
	status, api := env.Request(t, http.MethodPut, "/system/sensitive-word", admin, map[string]any{
		"id": rowID, "word": "缓存词", "status": 1,
	})
	mustOK(t, "停用敏感词", status, api)
	if _, words, _, _ := checkTextAPI(t, env, admin, "含缓存词文本"); len(words) != 0 {
		t.Fatalf("停用后不应命中，实际 %v", words)
	}

	// 重新启用后恢复命中。
	status, api = env.Request(t, http.MethodPut, "/system/sensitive-word", admin, map[string]any{
		"id": rowID, "word": "缓存词", "status": 0,
	})
	mustOK(t, "启用敏感词", status, api)
	if _, words, _, _ := checkTextAPI(t, env, admin, "含缓存词文本"); len(words) != 1 {
		t.Fatalf("启用后应恢复命中，实际 %v", words)
	}

	// 删除后不再命中。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/sensitive-word/%d", rowID), admin, nil)
	mustOK(t, "删除敏感词", status, api)
	if _, words, _, _ := checkTextAPI(t, env, admin, "含缓存词文本"); len(words) != 0 {
		t.Fatalf("删除后不应命中，实际 %v", words)
	}
}

// TestIntegrationSensitiveWordLimit 单租户上限 5000（spec §4.7.1）：
// 直插 4999 条后 API 还能加 1 条；第 5001 条 409；删除 1 条后可再加。
func TestIntegrationSensitiveWordLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 直插 4999 条（走租户插件，带租户上下文；批量写比逐条 HTTP 快得多）。
	ctx := tenant.WithTenantID(context.Background(), 1)
	rows := make([]model.SensitiveWord, 0, 4999)
	for i := 0; i < 4999; i++ {
		rows = append(rows, model.SensitiveWord{
			TenantID: 1,
			Word:     fmt.Sprintf("预置词%04d", i),
			Status:   model.StatusEnable,
		})
	}
	if err := env.DB.WithContext(ctx).CreateInBatches(rows, 500).Error; err != nil {
		t.Fatalf("批量预置敏感词: %v", err)
	}

	// 第 5000 条成功。
	mustCreateSensitiveWord(t, env, admin, "第5000条")
	// 第 5001 条 409。
	if status, msg := createSensitiveWordAPI(t, env, admin, "第5001条", nil); status != http.StatusConflict {
		t.Fatalf("超过上限应 409，实际 HTTP %d msg %q", status, msg)
	}

	// 删 1 条后腾出配额，可再添加。
	_, list := sensitiveWordPageTotal(t, env, admin, "&word=第5000条")
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/system/sensitive-word/%d", int64(list[0]["id"].(float64))), admin, nil)
	mustOK(t, "删除腾配额", status, api)
	mustCreateSensitiveWord(t, env, admin, "第5001条")
}

// TestIntegrationSensitiveWordTenantIsolation 跨租户词库隔离（spec §4.11.4）：
// 检测只看本租户词库；跨租户修改/删除业务 ID 一律 404；分页互不可见。
func TestIntegrationSensitiveWordTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	mustCreateSensitiveWord(t, env, platformAdmin, "租户甲独有词")

	tenantID := createTenantForPlatformTest(t, env, platformAdmin, "敏感词隔离租户")
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	// 租户乙建自己的词。
	mustCreateSensitiveWord(t, env, tenantAdmin, "租户乙独有词")

	// 租户乙检测：只命中本租户词，不命中租户甲的词。
	_, words, _, _ := checkTextAPI(t, env, tenantAdmin, "租户甲独有词 与 租户乙独有词")
	if len(words) != 1 || words[0] != "租户乙独有词" {
		t.Fatalf("租户乙应只命中本租户词，实际 %v", words)
	}
	// 平台（租户甲）检测：反之亦然。
	_, words, _, _ = checkTextAPI(t, env, platformAdmin, "租户甲独有词 与 租户乙独有词")
	if len(words) != 1 || words[0] != "租户甲独有词" {
		t.Fatalf("租户甲应只命中本租户词，实际 %v", words)
	}

	// 跨租户业务 ID：404。
	_, list := sensitiveWordPageTotal(t, env, platformAdmin, "&word=租户甲独有词")
	otherID := int64(list[0]["id"].(float64))
	status, api := env.Request(t, http.MethodPut, "/system/sensitive-word", tenantAdmin, map[string]any{
		"id": otherID, "word": "越权改", "status": 0,
	})
	if status != http.StatusNotFound {
		t.Fatalf("跨租户修改应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/sensitive-word/%d", otherID), tenantAdmin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户删除应 404，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 分页互不可见。
	tenantBTotal, _ := sensitiveWordPageTotal(t, env, tenantAdmin, "")
	if tenantBTotal != 1 {
		t.Fatalf("租户乙词库应只 1 条，实际 %d", tenantBTotal)
	}
}

// createCustomerForSensitiveTest 创建客户并返回 ID（跟进接入点用；crm_test
// 包的同名辅助不可跨包复用，这里用最小 payload 自建）。
func createCustomerForSensitiveTest(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/customer", token, map[string]any{"name": name})
	mustOK(t, "创建客户 "+name, status, api)
	var customer struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &customer); err != nil || customer.ID == 0 {
		t.Fatalf("解析创建客户响应: %v（data=%s）", err, string(api.Data))
	}
	return customer.ID
}

// customerLastFollowTimeForSensitiveTest 读客户详情返回 lastFollowTime（无则 nil）。
func customerLastFollowTimeForSensitiveTest(t *testing.T, env *testutil.IntegrationEnv, token string, customerID int64) any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/crm/customer/%d", customerID), token, nil)
	mustOK(t, "客户详情", status, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析客户详情: %v（data=%s）", err, string(api.Data))
	}
	return detail["lastFollowTime"]
}

// TestIntegrationSensitiveWordAnnouncement 公告接入点（spec §4.7.4）：
// 标题或正文命中 → 400「内容包含敏感词：…」且不落库；未命中正常创建/修改。
func TestIntegrationSensitiveWordAnnouncement(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	mustCreateSensitiveWord(t, env, admin, "敏感公告词")

	// 基线：当前公告总数。
	status, api := env.Request(t, http.MethodGet, "/oa/announcement/page?pageNo=1&pageSize=100", admin, nil)
	mustOK(t, "公告分页", status, api)
	var before struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &before); err != nil {
		t.Fatalf("解析公告分页: %v", err)
	}

	// 标题命中：400，msg 含命中词，公告数不变。
	status, api = env.Request(t, http.MethodPost, "/oa/announcement", admin, map[string]any{
		"title": "标题含敏感公告词", "content": "正文", "audienceType": 0,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("标题命中应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
	if !strings.Contains(api.Msg, "内容包含敏感词") || !strings.Contains(api.Msg, "敏感公告词") {
		t.Fatalf("错误信息应含「内容包含敏感词：敏感公告词」，实际 %q", api.Msg)
	}

	// 正文命中：同样 400。
	status, api = env.Request(t, http.MethodPost, "/oa/announcement", admin, map[string]any{
		"title": "干净标题", "content": "正文里藏了敏感公告词", "audienceType": 0,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("正文命中应 400，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 公告数不变：命中不落库。
	status, api = env.Request(t, http.MethodGet, "/oa/announcement/page?pageNo=1&pageSize=100", admin, nil)
	mustOK(t, "公告分页复查", status, api)
	var after struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &after); err != nil {
		t.Fatalf("解析公告分页: %v", err)
	}
	if after.Total != before.Total {
		t.Fatalf("命中后公告数不应变化，基线 %d 实际 %d", before.Total, after.Total)
	}

	// 未命中：创建成功；修改为干净内容成功。
	status, api = env.Request(t, http.MethodPost, "/oa/announcement", admin, map[string]any{
		"title": "正常公告", "content": "正常正文", "audienceType": 0,
	})
	mustOK(t, "未命中创建公告", status, api)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &created); err != nil || created.ID == 0 {
		t.Fatalf("解析创建公告响应: %v（data=%s）", err, string(api.Data))
	}

	status, api = env.Request(t, http.MethodPut, "/oa/announcement", admin, map[string]any{
		"id": created.ID, "title": "正常公告改", "content": "正文带敏感公告词", "audienceType": 0,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("修改命中应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, "/oa/announcement", admin, map[string]any{
		"id": created.ID, "title": "正常公告改", "content": "干净正文", "audienceType": 0,
	})
	mustOK(t, "未命中修改公告", status, api)
}

// TestIntegrationSensitiveWordFollowUp CRM 跟进接入点（spec §4.7.4）：
// 内容命中 → 400「内容包含敏感词：…」且不落库（last_follow_time 不刷新）；
// 未命中正常创建/修改。
func TestIntegrationSensitiveWordFollowUp(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	mustCreateSensitiveWord(t, env, admin, "敏感跟进词")

	customerID := createCustomerForSensitiveTest(t, env, admin, "敏感词跟进客户")

	// 内容命中：400 且不落库。
	status, api := env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"customerId": customerID, "content": "本次联系涉及敏感跟进词",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("跟进内容命中应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
	if !strings.Contains(api.Msg, "内容包含敏感词") || !strings.Contains(api.Msg, "敏感跟进词") {
		t.Fatalf("错误信息应含「内容包含敏感词：敏感跟进词」，实际 %q", api.Msg)
	}

	// 跟进未落库：分页为空。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/crm/follow-up/page?pageNo=1&pageSize=50&customerId=%d", customerID), admin, nil)
	mustOK(t, "跟进分页", status, api)
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析跟进分页: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("命中后跟进不应落库，实际 %d 条", page.Total)
	}
	// last_follow_time 未刷新（仍为空）。
	if got := customerLastFollowTimeForSensitiveTest(t, env, admin, customerID); got != nil {
		t.Fatalf("命中后 last_follow_time 不应刷新，实际 %v", got)
	}

	// 未命中：创建成功；修改命中 400、干净内容 200。
	status, api = env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"customerId": customerID, "content": "正常跟进内容",
	})
	mustOK(t, "未命中创建跟进", status, api)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &created); err != nil || created.ID == 0 {
		t.Fatalf("解析创建跟进响应: %v（data=%s）", err, string(api.Data))
	}

	status, api = env.Request(t, http.MethodPut, "/crm/follow-up", admin, map[string]any{
		"id": created.ID, "content": "改后涉及敏感跟进词",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("修改命中应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, "/crm/follow-up", admin, map[string]any{
		"id": created.ID, "content": "改后的干净内容",
	})
	mustOK(t, "未命中修改跟进", status, api)
}

// TestIntegrationSensitiveWordCheckBodyLimit 检测端点请求体收口（#85 安全审计
// NV3）：超过 MaxBytesReader（128KB）的请求体 400；字符数超 dto 上限
// （102400）但体量在硬上限内的文本同样 400；正常大小（50KB）不受影响。
func TestIntegrationSensitiveWordCheckBodyLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	mustCreateSensitiveWord(t, env, admin, "违禁")

	// 200KB 文本 → 请求体硬上限 400（超限文案提示上限）。
	status, _, _, msg := checkTextAPI(t, env, admin, strings.Repeat("a", 200<<10))
	if status != http.StatusBadRequest {
		t.Fatalf("200KB 文本：HTTP %d msg %q，期望 400", status, msg)
	}
	if !strings.Contains(msg, "请求体过大") {
		t.Fatalf("超限文案应提示请求体过大：%q", msg)
	}

	// 110K 字符（ASCII，110KB < 128KB 硬上限）→ dto 字符上限 400。
	status, _, _, msg = checkTextAPI(t, env, admin, strings.Repeat("a", 110_000))
	if status != http.StatusBadRequest {
		t.Fatalf("110K 字符文本：HTTP %d msg %q，期望 400", status, msg)
	}

	// 50KB 正常大小不受影响，检测照常返回。
	status, words, _, msg := checkTextAPI(t, env, admin, strings.Repeat("正文", 16000)+"违禁词尾")
	if status != http.StatusOK {
		t.Fatalf("50KB 正常文本：HTTP %d msg %q，期望 200", status, msg)
	}
	if len(words) != 1 || words[0] != "违禁" {
		t.Fatalf("正常大小检测应命中违禁词：%v", words)
	}
}
