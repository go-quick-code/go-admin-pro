//go:build integration

// mall 会员域 HTTP 集成测试（issue #174，docs/mall-mvp-spec.md §4/§8 会员行）：
//   - 会员 CRUD 全链路（创建/分页/详情/更新/停启用）；
//   - mobile 租户内唯一（同租户撞号 409、跨租户同号互不可见各自成功）；
//   - 密码 bcrypt 散列（明文不落库，更新空密码不改哈希）；
//   - 等级 CRUD + name/level 双唯一 409 + 被会员引用禁删 409 + 种子行；
//   - 按会员查地址（默认地址置顶、跨会员不可见）；
//   - 字段校验 400 与存在性 404。
//
// 401 探测网（internal/testutil protected_probe_test.go）经 prefixes 列表
// append /mall 自动覆盖新前缀的组归属；租户隔离由跨租户用例覆盖。
package mall_test

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// memberHashFromDB 读会员 DB 行的 password_hash（散列断言用）。
func memberHashFromDB(t *testing.T, env *testutil.IntegrationEnv, id int64) string {
	t.Helper()
	var hash string
	if err := tenant.Skip(env.DB).Raw(
		"SELECT password_hash FROM member_user WHERE id = ?", id,
	).Scan(&hash).Error; err != nil || hash == "" {
		t.Fatalf("读会员 %d password_hash: %v（hash=%q）", id, err, hash)
	}
	return hash
}

// TestIntegrationMemberCRUD 会员全链路：创建（带密码）→ 分页 → 详情 →
// 更新（空密码不改哈希）→ 重设密码（哈希变化且 bcrypt 可验证）。
func TestIntegrationMemberCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 创建：mobile + password + 昵称缺省取手机号。
	created := mallCreateMember(t, env, token, map[string]any{
		"mobile": "13800000101", "password": "secret66", "nickname": "张三",
	})
	id := int64(created["id"].(float64))
	if id == 0 {
		t.Fatalf("创建会员响应缺少 id：%s", created)
	}

	// 密码 bcrypt 散列（明文不落库），bcrypt 可验证。
	hash := memberHashFromDB(t, env, id)
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("secret66")); err != nil {
		t.Fatalf("password_hash 应可被 bcrypt 验证 secret66：%v（hash=%q）", err, hash)
	}
	if strings.Contains(hash, "secret66") {
		t.Fatalf("password_hash 不得包含明文：%q", hash)
	}

	// 分页：mobile 精确过滤命中。
	status, api := env.Request(t, http.MethodGet, "/mall/member/page?mobile=13800000101&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "会员分页", status, api)
	list := mallPageList(t, api)
	if len(list) != 1 || int64(list[0]["id"].(float64)) != id {
		t.Fatalf("会员分页应唯一命中 id=%d，实际 %v", id, list)
	}

	// 详情：字段回读。
	status, api = env.Request(t, http.MethodGet, "/mall/member/page", token, nil)
	mustOK(t, "会员分页（无过滤）", status, api)
	status, api = env.Request(t, http.MethodGet, "", token, nil)
	_ = status // 占位防误用（detail 用下方的 GET /mall/member/:id）。
	status, api = env.Request(t, http.MethodGet, detailPath(id), token, nil)
	mustOK(t, "会员详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if detail["mobile"] != "13800000101" || detail["nickname"] != "张三" {
		t.Fatalf("会员详情字段不符：%s", api.Data)
	}

	// 更新：昵称/备注/等级（等级先建），password 留空 = 不改哈希。
	levelID := mallCreateLevel(t, env, token, "黄金会员", 2)
	status, api = env.Request(t, http.MethodPut, "/mall/member", token, map[string]any{
		"id": id, "mobile": "13800000101", "nickname": "张三丰",
		"levelId": levelID, "remark": "老会员",
	})
	mustOK(t, "会员更新", status, api)
	hash2 := memberHashFromDB(t, env, id)
	if hash2 != hash {
		t.Fatalf("更新留空密码不得改哈希：before=%q after=%q", hash, hash2)
	}
	status, api = env.Request(t, http.MethodGet, detailPath(id), token, nil)
	mustOK(t, "会员详情（更新后）", status, api)
	detail = parseJSON(t, string(api.Data))
	if detail["nickname"] != "张三丰" || detail["remark"] != "老会员" {
		t.Fatalf("会员更新未生效：%s", api.Data)
	}
	if lvl, ok := detail["levelId"].(float64); !ok || int64(lvl) != levelID {
		t.Fatalf("会员更新 levelId 未生效：%s", api.Data)
	}

	// 重设密码：哈希变化且新密码可验证、旧密码失效。
	status, api = env.Request(t, http.MethodPut, "/mall/member", token, map[string]any{
		"id": id, "mobile": "13800000101", "password": "newpass9",
	})
	mustOK(t, "会员重设密码", status, api)
	hash3 := memberHashFromDB(t, env, id)
	if hash3 == hash {
		t.Fatalf("重设密码后哈希应变化")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash3), []byte("newpass9")); err != nil {
		t.Fatalf("新密码 bcrypt 验证失败：%v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash3), []byte("secret66")); err == nil {
		t.Fatalf("旧密码不应仍可验证")
	}
}

// detailPath 会员详情路径。
func detailPath(id int64) string {
	return "/mall/member/" + itoa(id)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// TestIntegrationMemberMobileUnique409 mobile 租户内唯一：同租户撞号 409；
// 跨租户同号互不可见、各自成功（租户插件隔离 + UNIQUE(tenant_id, mobile)）。
func TestIntegrationMemberMobileUnique409(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	mallCreateMember(t, env, adminToken, map[string]any{"mobile": "13800000201"})

	// 同租户撞号：409。
	status, api := env.Request(t, http.MethodPost, "/mall/member", adminToken, map[string]any{
		"mobile": "13800000201", "nickname": "撞号",
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("同租户撞号：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 跨租户：新建租户，同号注册成功；且看不到租户 1 的会员（租户隔离）。
	tenant2 := createTenantByAPI(t, env, adminToken, "mall-unique-t2")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	created := mallCreateMember(t, env, token2, map[string]any{"mobile": "13800000201"})
	if int64(created["id"].(float64)) == 0 {
		t.Fatalf("跨租户同号注册应成功：%s", created)
	}
	// 租户 2 分页只看到自己的会员。
	status, api = env.Request(t, http.MethodGet, "/mall/member/page?pageNo=1&pageSize=50", token2, nil)
	mustOK(t, "租户 2 会员分页", status, api)
	list := mallPageList(t, api)
	if len(list) != 1 || list[0]["mobile"] != "13800000201" {
		t.Fatalf("租户 2 分页应只见自己 1 个会员，实际 %v", list)
	}
	// 租户 2 读租户 1 会员详情 → 404（跨租户业务 ID 不可见，不泄露存在性）。
	tenant1Member := mallCreateMember(t, env, adminToken, map[string]any{"mobile": "13800000202"})
	status, api = env.Request(t, http.MethodGet, detailPath(int64(tenant1Member["id"].(float64))), token2, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户详情：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	// 反向：admin（租户 1）读租户 2 的会员 → 同样 404。
	status, api = env.Request(t, http.MethodGet, detailPath(int64(created["id"].(float64))), adminToken, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("反向跨租户详情：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
}

// TestIntegrationMemberStatusToggle 停启用：置停用后 status 过滤可命中、
// 非法状态 400、未知会员 404。
func TestIntegrationMemberStatusToggle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	created := mallCreateMember(t, env, token, map[string]any{"mobile": "13800000301"})
	id := int64(created["id"].(float64))

	// 停用（status=1）。
	status, api := env.Request(t, http.MethodPut, statusPath(id), token, map[string]any{"status": 1})
	mustOK(t, "停用会员", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/member/page?status=1&pageNo=1&pageSize=50", token, nil)
	mustOK(t, "按停用过滤", status, api)
	list := mallPageList(t, api)
	if len(list) != 1 || int64(list[0]["id"].(float64)) != id {
		t.Fatalf("停用过滤应命中 id=%d，实际 %v", id, list)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/member/page?status=0&pageNo=1&pageSize=50", token, nil)
	mustOK(t, "按启用过滤", status, api)
	for _, row := range mallPageList(t, api) {
		if int64(row["id"].(float64)) == id {
			t.Fatalf("会员 %d 已停用，不应出现在启用过滤", id)
		}
	}

	// 启用回置（status=0：非指针零值合法性由 DTO 指针承载）。
	status, api = env.Request(t, http.MethodPut, statusPath(id), token, map[string]any{"status": 0})
	mustOK(t, "启用会员", status, api)

	// 非法状态：400。
	status, api = env.Request(t, http.MethodPut, statusPath(id), token, map[string]any{"status": 2})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("非法状态：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 未知会员：404。
	status, api = env.Request(t, http.MethodPut, statusPath(999999), token, map[string]any{"status": 1})
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("未知会员停启用：HTTP %d code %d，期望 404", status, api.Code)
	}
}

func statusPath(id int64) string {
	return detailPath(id) + "/status"
}

// TestIntegrationMemberValidation 字段校验：非法手机号/短密码 400、
// 不存在等级 404。
func TestIntegrationMemberValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	cases := []struct {
		name string
		body map[string]any
		code int
	}{
		{"非法手机号", map[string]any{"mobile": "12345"}, 400},
		{"空手机号", map[string]any{"mobile": ""}, 400},
		{"密码过短", map[string]any{"mobile": "13800000401", "password": "12345"}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, api := env.Request(t, http.MethodPost, "/mall/member", token, tc.body)
			if status != http.StatusBadRequest || api.Code != 400 {
				t.Fatalf("%s：HTTP %d code %d，期望 400", tc.name, status, api.Code)
			}
		})
	}
	// 不存在等级：404。
	status, api := env.Request(t, http.MethodPost, "/mall/member", token, map[string]any{
		"mobile": "13800000402", "levelId": 999999,
	})
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("不存在等级：HTTP %d code %d，期望 404", status, api.Code)
	}
}
