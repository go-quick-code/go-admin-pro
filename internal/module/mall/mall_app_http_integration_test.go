//go:build integration

// mall C 端公开端点 HTTP 集成测试（issue #178，T5；docs/mall-mvp-spec.md
// §2/§4/§7 裁决 2/6）：
//   - 注册/登录/我的信息：注册自动建会员（默认等级「普通会员」level=1、
//     point_balance 0、status 启用）+ 自动登录发令牌；mobile 租户内唯一
//     409；密码 6–32 界；登录失败统一文案（不存在/错密码/无密码/停用同答，
//     不泄露存在性）；/me 返回基本信息+等级名+point_balance；
//   - 令牌失败矩阵（签名/过期/停用/跨租户经 DB 回查失效，无撤销列表）：
//     无令牌 401、伪造签名 401、篡改 payload 401、过期 401、停用会员 401、
//     tid↔mid 绑定错乱 401——全部统一「登录状态已失效，请重新登录」；
//   - 上架过滤：分页只含上架 SPU（detail 大字段不出列表），详情对下架/
//     不存在统一 404（不泄露存在性），SKU 全量（价格/库存/规格快照）；
//   - 限速：注册/登录 10 次/分钟合并计数（超限 429，照 pay demo 回调先例
//     响应形状）、浏览端点 60 次/分钟。
package mall_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// appRegister C 端注册（返回原始响应供统一文案/状态码断言）。
func appRegister(t *testing.T, env *testutil.IntegrationEnv, body map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/mall/app/auth/register", "", body)
}

// appLogin C 端登录（返回原始响应供统一文案/状态码断言）。
func appLogin(t *testing.T, env *testutil.IntegrationEnv, body map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/mall/app/auth/login", "", body)
}

// appAuth parses 注册/登录响应的 data（accessToken + member 档案）。
func appAuth(t *testing.T, api testutil.APIResponse) (string, map[string]any) {
	t.Helper()
	data := parseJSON(t, string(api.Data))
	token, _ := data["accessToken"].(string)
	if token == "" {
		t.Fatalf("注册/登录响应缺少 accessToken：%s", api.Data)
	}
	member, _ := data["member"].(map[string]any)
	if member == nil {
		t.Fatalf("注册/登录响应缺少 member 档案：%s", api.Data)
	}
	return token, member
}

// signAppMemberToken 用指定密钥构造会员令牌（测试直构——失败矩阵的过期/
// 跨租户/伪造形态需要精确控制 payload 与签名材料，登录端点只发得出「此刻
// 有效」的令牌）。
func signAppMemberToken(t *testing.T, env *testutil.IntegrationEnv, secret string, memberID, tenantID int64, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"mid": memberID, "tid": tenantID, "exp": exp.Unix(),
	})
	if err != nil {
		t.Fatalf("构造令牌 payload: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded)) // hash.Write 不返回错误
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// TestIntegrationAppRegisterLoginMe 注册/登录/我的信息全链路 + 注册唯一 409
// + 密码界 + 登录失败统一文案。
//
// 桶计数注意：本用例 auth 面（注册+登录）恰好 10 次请求——auth 限速桶
// 上限内（10 次/分钟，第 10 次放行）；后续 /me 请求走浏览桶（60/分钟），
// 不占 auth 桶。增删 auth 面请求时同步核对桶计数，超限会以 429 假红。
func TestIntegrationAppRegisterLoginMe(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	const mobile = "13890001001"
	const password = "secret66"

	// 注册：自动建会员 + 自动登录（令牌 + 档案）。
	status, api := appRegister(t, env, map[string]any{"mobile": mobile, "password": password, "nickname": "王五"})
	mustOK(t, "注册", status, api)
	token, member := appAuth(t, api)
	if member["mobile"] != mobile || member["nickname"] != "王五" {
		t.Fatalf("注册档案字段不符：%s", api.Data)
	}
	// 默认等级「普通会员」按 level=1 查询（spec §3.1 种子行）+ 初始积分 0
	// + 启用状态（服务端定，客户端不采纳任何状态字段）。
	if member["levelName"] != "普通会员" || member["pointBalance"] != float64(0) || member["status"] != float64(0) {
		t.Fatalf("注册默认态不符（等级/积分/状态）：%s", api.Data)
	}
	memberID := int64(member["id"].(float64))

	// bcrypt 落库口径照管理端：明文不落库（memberHashFromDB 复用 T1 镜像）。
	if hash := memberHashFromDB(t, env, memberID); !strings.HasPrefix(hash, "$2") || strings.Contains(hash, password) {
		t.Fatalf("password_hash 应为 bcrypt 哈希且不含明文：%q", hash)
	}

	// 注册唯一 409（同 mobile 再注册；昵称不同也不放行）。
	status, api = appRegister(t, env, map[string]any{"mobile": mobile, "password": password, "nickname": "李四"})
	if status != http.StatusConflict || api.Code != 409 || !strings.Contains(api.Msg, "手机号已存在") {
		t.Fatalf("重复注册应 409「手机号已存在」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 密码强度界（6–32）：5 位 400、33 位 400。
	status, api = appRegister(t, env, map[string]any{"mobile": "13890001002", "password": "12345"})
	if status != http.StatusBadRequest {
		t.Fatalf("5 位密码应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = appRegister(t, env, map[string]any{"mobile": "13890001002", "password": strings.Repeat("p", 33)})
	if status != http.StatusBadRequest {
		t.Fatalf("33 位密码应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 登录失败统一文案：不存在 / 错密码 / 管理端建号无密码 / 停用——四种
	// 形态同答（HTTP 200 + 业务码 1004「账号或密码错误」，照 system login
	// 信封；不泄露存在性）。
	const uniformMsg = "账号或密码错误"
	status, api = appLogin(t, env, map[string]any{"mobile": "13890009999", "password": password})
	if status != http.StatusOK || api.Code != 1004 || api.Msg != uniformMsg {
		t.Fatalf("登录不存在手机号应统一文案，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	baseline := api.Msg
	status, api = appLogin(t, env, map[string]any{"mobile": mobile, "password": "wrong-pass"})
	if status != http.StatusOK || api.Code != 1004 || api.Msg != baseline {
		t.Fatalf("错密码与不存在手机号必须同响应（统一文案），实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 管理端建号不设密码：同答。
	mallCreateMember(t, env, adminToken, map[string]any{"mobile": "13890001003"})
	status, api = appLogin(t, env, map[string]any{"mobile": "13890001003", "password": password})
	if status != http.StatusOK || api.Code != 1004 || api.Msg != baseline {
		t.Fatalf("无密码会员登录与错密码必须同响应，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 停用会员：同答（响应「账号已停用」即泄露存在性）。
	status, api = appRegister(t, env, map[string]any{"mobile": "13890001004", "password": password})
	mustOK(t, "注册停用样本", status, api)
	disabledID := int64(parseJSON(t, string(api.Data))["member"].(map[string]any)["id"].(float64))
	status, api = env.Request(t, http.MethodPut, "/mall/member/"+itoa(disabledID)+"/status", adminToken, map[string]any{"status": mallmodel.StatusDisable})
	mustOK(t, "停用会员", status, api)
	status, api = appLogin(t, env, map[string]any{"mobile": "13890001004", "password": password})
	if status != http.StatusOK || api.Code != 1004 || api.Msg != baseline {
		t.Fatalf("停用会员登录必须与错密码同响应，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 登录成功换新令牌；/me 返回基本信息+等级名+point_balance。
	status, api = appLogin(t, env, map[string]any{"mobile": " " + mobile + " ", "password": password})
	mustOK(t, "登录（mobile 带首尾空白应归一化）", status, api)
	token2, _ := appAuth(t, api)
	if token2 == "" {
		t.Fatalf("登录应签发令牌：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/auth/me", token2, nil)
	mustOK(t, "我的信息", status, api)
	me := parseJSON(t, string(api.Data))
	if me["mobile"] != mobile || me["levelName"] != "普通会员" || me["pointBalance"] != float64(0) {
		t.Fatalf("/me 字段不符：%s", api.Data)
	}
	if me["passwordHash"] != nil || me["remark"] != nil {
		t.Fatalf("/me 不得携带管理面字段（password_hash/remark）：%s", api.Data)
	}

	// 无令牌 401（统一文案，与校验失败同答）。
	status, api = env.Request(t, http.MethodGet, "/mall/app/auth/me", "", nil)
	if status != http.StatusUnauthorized || api.Code != 401 || api.Msg != mallservice.MemberTokenUnauthorizedMsg {
		t.Fatalf("无令牌 /me 应 401 统一文案，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 注册自动登录的令牌与登录令牌都有效（/me 双令牌均可达）。
	for i, tk := range []string{token, token2} {
		if status, _ := env.Request(t, http.MethodGet, "/mall/app/auth/me", tk, nil); status != http.StatusOK {
			t.Fatalf("令牌 %d 应可访问 /me，实际 HTTP %d", i, status)
		}
	}
}

// TestIntegrationAppTokenMatrix 令牌失败矩阵：伪造签名 / 篡改 payload /
// 过期 / 停用会员 / 跨租户（tid↔mid 绑定）全部 401 统一文案——校验经 DB
// 查 member_user（tenant 一致 + status 启用），无撤销列表。
func TestIntegrationAppTokenMatrix(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	// 真实令牌（注册自动登录）+ 会员 ID。C 端注册落默认租户（T5 单租户口径）。
	status, api := appRegister(t, env, map[string]any{"mobile": "13890002101", "password": "secret66"})
	mustOK(t, "注册", status, api)
	token, member := appAuth(t, api)
	memberID := int64(member["id"].(float64))
	defaultTenantID := int64(tenant.DefaultID)

	// 跨租户样本：租户 2 管理员建号设密码（管理端入口），得到 tid2↔mid2。
	tenant2ID := createTenantByAPI(t, env, adminToken, "mall-t5-token-matrix")
	token2 := loginTenant(t, env, "admin", "123456", tenant2ID)
	status, api = env.Request(t, http.MethodPost, "/mall/member", token2, map[string]any{
		"mobile": "13890002102", "password": "secret66",
	})
	mustOK(t, "租户 2 建会员", status, api)
	member2ID := int64(parseJSON(t, string(api.Data))["id"].(float64))

	// 篡改 payload：真实令牌的 mid 换成 memberID+1，签名原样保留——签名
	// 恒时比对必失败（payload 变了签名没变）。
	tamperPayload := func() string {
		parts := strings.SplitN(token, ".", 2)
		raw, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			t.Fatalf("解码真实令牌 payload: %v", err)
		}
		var claims map[string]any
		if err := json.Unmarshal(raw, &claims); err != nil {
			t.Fatalf("解析真实令牌 payload: %v", err)
		}
		claims["mid"] = claims["mid"].(float64) + 1
		mutated, err := json.Marshal(claims)
		if err != nil {
			t.Fatalf("重编码篡改 payload: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(mutated) + "." + parts[1]
	}()
	// 伪造签名：payload 指向真实会员（若签名校验失效，本令牌将直达 200），
	// 但用攻击者密钥签名——签名比对必须拦截。
	now := time.Now()
	forged := signAppMemberToken(t, env, "attacker-chosen-key", memberID, defaultTenantID, now.Add(time.Hour))

	cases := []struct {
		name  string
		token string
	}{
		{"伪造签名（密钥不符）", forged},
		{"篡改 payload（签名不重算）", tamperPayload},
		{"过期令牌（签名合法）", signAppMemberToken(t, env, env.Config.Mall.JWTSecret, memberID, defaultTenantID, now.Add(-time.Minute))},
		{"跨租户 tid（member 不在 tid 租户）", signAppMemberToken(t, env, env.Config.Mall.JWTSecret, memberID, 987654, now.Add(time.Hour))},
		{"跨租户 mid↔tid 顶替（租户 2 会员挂租户 1 tid）", signAppMemberToken(t, env, env.Config.Mall.JWTSecret, member2ID, defaultTenantID, now.Add(time.Hour))},
		{"垃圾令牌（非两段式）", "not-a-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, api := env.Request(t, http.MethodGet, "/mall/app/auth/me", tc.token, nil)
			if status != http.StatusUnauthorized || api.Code != 401 {
				t.Fatalf("%s 应 401，实际 HTTP %d code %d msg %q", tc.name, status, api.Code, api.Msg)
			}
			if api.Msg != mallservice.MemberTokenUnauthorizedMsg {
				t.Fatalf("%s 应统一文案（不泄露失败形态），实际 %q", tc.name, api.Msg)
			}
		})
	}

	// 停用会员：真实令牌即时失效（每次校验 DB 查 status，无撤销列表）；
	// 重新启用即恢复——失效语义随 DB 状态而非签发时快照。
	status, api = env.Request(t, http.MethodPut, "/mall/member/"+itoa(memberID)+"/status", adminToken, map[string]any{"status": mallmodel.StatusDisable})
	mustOK(t, "停用会员", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/app/auth/me", token, nil)
	if status != http.StatusUnauthorized || api.Code != 401 {
		t.Fatalf("停用会员令牌应 401，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, "/mall/member/"+itoa(memberID)+"/status", adminToken, map[string]any{"status": mallmodel.StatusEnable})
	mustOK(t, "重新启用会员", status, api)
	if status, _ := env.Request(t, http.MethodGet, "/mall/app/auth/me", token, nil); status != http.StatusOK {
		t.Fatalf("重新启用后原令牌应恢复有效，实际 HTTP %d", status)
	}
}

// TestIntegrationAppProductBrowse C 端商品浏览：分页恒上架（不含下架，
// 不含 detail 大字段）、分类/品牌/关键词过滤、详情 SKU 全量、下架/不存在
// 404 统一文案、上下架边界即时生效。
func TestIntegrationAppProductBrowse(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	// 主数据 + 两个上架一个下架的 SPU（下架走默认态：新建先入库再上架）。
	categoryID := mallCreateCategory(t, env, adminToken, map[string]any{"name": "图书"})
	category2ID := mallCreateCategory(t, env, adminToken, map[string]any{"name": "文具"})
	status, api := env.Request(t, http.MethodPost, "/mall/product/brand", adminToken, map[string]any{"name": "MallT5Brand"})
	mustOK(t, "创建品牌", status, api)
	brandID := int64(parseJSON(t, string(api.Data))["id"].(float64))

	onSPU := mallCreateSPU(t, env, adminToken, map[string]any{
		"categoryId": categoryID, "brandId": brandID, "name": "上架书A",
		"detail": "<p>上架书A图文详情</p>", "specType": 0, "marketPrice": "59.00",
		"status": 1,
		"skus":   []map[string]any{{"price": "49.00", "marketPrice": "59.00", "stock": 100}},
	})
	offSPU := mallCreateSPU(t, env, adminToken, map[string]any{
		"categoryId": categoryID, "brandId": brandID, "name": "下架书B",
		"specType": 0, "marketPrice": "39.00",
		"skus": []map[string]any{{"price": "29.00", "stock": 5}},
	})
	otherSPU := mallCreateSPU(t, env, adminToken, map[string]any{
		"categoryId": category2ID, "name": "文具C", "specType": 0, "status": 1,
		"skus": []map[string]any{{"price": "9.90", "stock": 50}},
	})
	onID := int64(onSPU["id"].(float64))
	offID := int64(offSPU["id"].(float64))
	otherID := int64(otherSPU["id"].(float64))

	// 会员令牌（浏览需登录——issue 口径「带令牌访问商品分页/详情」）。
	status, api = appRegister(t, env, map[string]any{"mobile": "13890003101", "password": "secret66"})
	mustOK(t, "注册会员", status, api)
	token, _ := appAuth(t, api)

	// 分页：只含上架，不含下架，列表项不带 detail 大字段。
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/page?pageNo=1&pageSize=50", token, nil)
	mustOK(t, "C 端商品分页", status, api)
	items := mallPageList(t, api)
	if len(items) != 2 {
		t.Fatalf("分页应只含 2 个上架 SPU，实际 %d：%s", len(items), api.Data)
	}
	names := map[string]bool{}
	for _, item := range items {
		names[item["name"].(string)] = true
		if _, has := item["detail"]; has {
			t.Fatalf("C 端分页项不得携带 detail 大字段：%s", api.Data)
		}
	}
	if !names["上架书A"] || !names["文具C"] || names["下架书B"] {
		t.Fatalf("分页应含上架书A/文具C 且不含下架书B：%v", names)
	}

	// 过滤：关键词 / 分类 / 品牌。
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/page?pageNo=1&pageSize=50&keyword=上架书", token, nil)
	mustOK(t, "关键词过滤", status, api)
	if items = mallPageList(t, api); len(items) != 1 || items[0]["name"] != "上架书A" {
		t.Fatalf("关键词过滤应只命中上架书A：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/page?pageNo=1&pageSize=50&categoryId="+itoa(categoryID), token, nil)
	mustOK(t, "分类过滤", status, api)
	if items = mallPageList(t, api); len(items) != 1 {
		t.Fatalf("分类过滤应只命中图书段上架 SPU（下架不计），实际 %d：%s", len(items), api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/page?pageNo=1&pageSize=50&brandId="+itoa(brandID), token, nil)
	mustOK(t, "品牌过滤", status, api)
	if items = mallPageList(t, api); len(items) != 1 || items[0]["name"] != "上架书A" {
		t.Fatalf("品牌过滤应只命中上架书A（下架书B 同品牌但下架不计）：%s", api.Data)
	}

	// 详情：SKU 全量（价格/库存/规格快照）+ detail 图文。
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/"+itoa(onID), token, nil)
	mustOK(t, "C 端商品详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if detail["name"] != "上架书A" || detail["detail"] != "<p>上架书A图文详情</p>" {
		t.Fatalf("详情应含 name 与 detail 图文：%s", api.Data)
	}
	skus, ok := detail["skus"].([]any)
	if !ok || len(skus) != 1 {
		t.Fatalf("详情应含 SKU 全量：%s", api.Data)
	}
	sku := skus[0].(map[string]any)
	if sku["price"] != "49.00" || sku["stock"] != float64(100) || sku["first"] != float64(1) {
		t.Fatalf("SKU 价格/库存/主 SKU 标记不符：%s", api.Data)
	}
	if props, ok := sku["properties"].([]any); !ok || len(props) != 0 {
		t.Fatalf("单规格 SKU properties 应为空数组：%s", api.Data)
	}

	// 下架/不存在统一 404「商品不存在」（不泄露存在性）；下架后分页即时
	// 消失（上架过滤是查询谓词，不是快照）。
	for _, tc := range []struct {
		name string
		id   int64
	}{
		{"下架商品", offID},
		{"不存在商品", 999999},
	} {
		status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/"+itoa(tc.id), token, nil)
		if status != http.StatusNotFound || api.Code != 404 || api.Msg != "商品不存在" {
			t.Fatalf("%s 应 404「商品不存在」，实际 HTTP %d code %d msg %q", tc.name, status, api.Code, api.Msg)
		}
	}

	// 上下架边界即时生效：管理端把上架书A下架 → C 端分页与详情都不可见；
	// 重新上架恢复。
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu/"+itoa(onID)+"/status", adminToken, map[string]any{"status": mallmodel.ProductSPUStatusOff})
	mustOK(t, "管理端下架上架书A", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/"+itoa(onID), token, nil)
	if status != http.StatusNotFound || api.Code != 404 || api.Msg != "商品不存在" {
		t.Fatalf("刚下架的商品详情应 404，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/product/spu/page?pageNo=1&pageSize=50", token, nil)
	mustOK(t, "下架后分页", status, api)
	for _, item := range mallPageList(t, api) {
		if item["name"] == "上架书A" {
			t.Fatalf("下架商品不得出现在 C 端分页：%s", api.Data)
		}
	}
	// 管理端分页不受上架过滤（两个 SPU 都可见）——管理/两端口径分界。
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/page?pageNo=1&pageSize=50", adminToken, nil)
	mustOK(t, "管理端分页", status, api)
	if items = mallPageList(t, api); len(items) < 3 {
		t.Fatalf("管理端分页应含全部 SPU（含下架），实际 %d", len(items))
	}
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu/"+itoa(onID)+"/status", adminToken, map[string]any{"status": mallmodel.ProductSPUStatusOn})
	mustOK(t, "重新上架", status, api)
	if status, _ := env.Request(t, http.MethodGet, "/mall/app/product/spu/"+itoa(otherID), token, nil); status != http.StatusOK {
		t.Fatalf("仍在架商品详情应 200，实际 HTTP %d", status)
	}
}

// TestIntegrationAppRateLimit 公开端点限速：注册/登录 10 次/分钟同桶合并
// 计数（超限 429，照 pay demo 回调先例的响应形状 {code:429,msg}——合法
// 请求同样被拦）；浏览端点 60 次/分钟独立计数。每用例独立 env 即独立计数。
func TestIntegrationAppRateLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 先注册两个样本（占 auth 桶 2 次）——浏览样本必须在烧桶前注册，否则
	// 会被自己用例的桶满拦住。
	status, api := appRegister(t, env, map[string]any{"mobile": "13890004101", "password": "secret66"})
	mustOK(t, "注册", status, api)
	status, api = appRegister(t, env, map[string]any{"mobile": "13890004102", "password": "secret66"})
	mustOK(t, "注册浏览样本", status, api)
	token, _ := appAuth(t, api)

	// 烧 auth 桶到 10：2 次成功注册 + 8 次非法注册（binding 400 各计 1 次）。
	for i := 0; i < 8; i++ {
		status, _ = appRegister(t, env, map[string]any{"mobile": "1389000420" + itoa(int64(i))})
		if status != http.StatusBadRequest {
			t.Fatalf("非法注册样本应 400，实际 HTTP %d", status)
		}
	}
	// 桶满：合法登录也被拦（同凭证面同桶——429 形状照 pay 先例）。
	status, api = appLogin(t, env, map[string]any{"mobile": "13890004101", "password": "secret66"})
	if status != http.StatusTooManyRequests || api.Code != 429 || api.Msg != "请求过于频繁，请稍后重试" {
		t.Fatalf("auth 桶满应 429 统一文案，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 浏览桶独立：auth 桶满不影响令牌端点；60 次 /me 放行后第 61 次 429。
	for i := 0; i < 60; i++ {
		if status, _ = env.Request(t, http.MethodGet, "/mall/app/auth/me", token, nil); status != http.StatusOK {
			t.Fatalf("浏览桶第 %d 次 /me 应 200，实际 HTTP %d", i+1, status)
		}
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/auth/me", token, nil)
	if status != http.StatusTooManyRequests || api.Code != 429 {
		t.Fatalf("浏览桶满应 429，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}
