//go:build integration

// 地区与 IP 定位集成测试（issue #82，spec §3.9/§4.8/§5.7）：走真实 router +
// JWT/权限/租户中间件 + 真实 MySQL，在 HTTP 契约上验证：
//   - 种子幂等：seed.Areas 重复写入不产生重复行，省市两级齐全；
//   - 树/子节点接口按 parentCode 语义返回（空 → 省级）；
//   - 导入：父子校验（库里已有或同批出现）、按 code upsert、重复导入不重复、
//     单次 ≤5000、非平台租户 403；
//   - 读取开放给所有登录用户（spec §4.8.1），导入需要权限码。
package system_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

type areaItem struct {
	Code       string `json:"code"`
	ParentCode string `json:"parentCode"`
	Name       string `json:"name"`
	Level      int    `json:"level"`
}

func areaMustOK(t *testing.T, label string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", label, status, api.Code, api.Msg)
	}
}

func areaCount(t *testing.T, env *testutil.IntegrationEnv) int64 {
	t.Helper()
	var count int64
	if err := env.DB.Table("system_area").Count(&count).Error; err != nil {
		t.Fatalf("统计 system_area 行数: %v", err)
	}
	return count
}

// TestIntegrationAreaSeedIdempotent 种子幂等：启动时已写入省市两级，
// 重复执行 seed.Areas 行数与内容不变。
func TestIntegrationAreaSeedIdempotent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	before := areaCount(t, env)
	if before == 0 {
		t.Fatal("启动种子应写入省市两级地区数据")
	}
	for i := 0; i < 2; i++ {
		if err := seed.Areas(env.DB); err != nil {
			t.Fatalf("重复执行种子: %v", err)
		}
	}
	if after := areaCount(t, env); after != before {
		t.Fatalf("种子幂等被破坏：重复写入后行数 %d，期望仍为 %d", after, before)
	}

	// 省级齐全（34 个省级行政区），层级与父编码正确。
	token := env.Login(t, "admin", "123456")
	status, api := env.Request(t, http.MethodGet, "/system/area/children", token, nil)
	areaMustOK(t, "查询省级列表", status, api)
	var provinces []areaItem
	if err := json.Unmarshal(api.Data, &provinces); err != nil {
		t.Fatalf("解析省级列表: %v（data=%s）", err, string(api.Data))
	}
	if len(provinces) != 34 {
		t.Fatalf("省级应为 34 项，实际 %d 项", len(provinces))
	}
	for _, p := range provinces {
		if p.Level != 1 || p.ParentCode != "" {
			t.Fatalf("省级行 %s 层级/父编码错误：level=%d parentCode=%q", p.Code, p.Level, p.ParentCode)
		}
	}
	// 市级合计 > 300 且全部 level=2、parentCode 指向省级。
	var cityTotal int
	for _, code := range []string{"110000", "130000", "440000", "650000"} {
		status, api = env.Request(t, http.MethodGet, "/system/area/children?parentCode="+code, token, nil)
		areaMustOK(t, "查询 "+code+" 子级", status, api)
		var cities []areaItem
		if err := json.Unmarshal(api.Data, &cities); err != nil {
			t.Fatalf("解析子级: %v", err)
		}
		if len(cities) == 0 {
			t.Fatalf("省份 %s 应有市级子节点", code)
		}
		cityTotal += len(cities)
		for _, c := range cities {
			if c.Level != 2 || c.ParentCode != code {
				t.Fatalf("市级行 %+v 层级/父编码错误", c)
			}
		}
	}
	if cityTotal < 20 {
		t.Fatalf("抽查四省的市级数量过少：%d", cityTotal)
	}
}

// TestIntegrationAreaChildrenSemantics 子节点接口的 parentCode 语义：
// 空 → 省级；存在的编码 → 其直接子级（平铺、不含孙级）；未知编码 → 空列表。
func TestIntegrationAreaChildrenSemantics(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 空 parentCode → 省级。
	status, api := env.Request(t, http.MethodGet, "/system/area/children", token, nil)
	areaMustOK(t, "空 parentCode", status, api)
	var provinces []areaItem
	_ = json.Unmarshal(api.Data, &provinces)
	if len(provinces) != 34 {
		t.Fatalf("空 parentCode 应返回省级 34 项，实际 %d", len(provinces))
	}

	// parentCode=130000（河北省）→ 11 个地级市，平铺无嵌套。
	status, api = env.Request(t, http.MethodGet, "/system/area/children?parentCode=130000", token, nil)
	areaMustOK(t, "河北省子级", status, api)
	var cities []areaItem
	if err := json.Unmarshal(api.Data, &cities); err != nil {
		t.Fatalf("解析河北省子级: %v", err)
	}
	if len(cities) != 11 {
		t.Fatalf("河北省应有 11 个地级市，实际 %d（data=%s）", len(cities), string(api.Data))
	}
	if cities[0].Code != "130100" || cities[0].Name != "石家庄市" {
		t.Fatalf("河北省第一个市应为 130100 石家庄市，实际 %+v", cities[0])
	}

	// 未知编码 → 空数组（非 null、非错误）。
	status, api = env.Request(t, http.MethodGet, "/system/area/children?parentCode=999999", token, nil)
	areaMustOK(t, "未知编码", status, api)
	if !strings.Contains(string(api.Data), "[]") {
		t.Fatalf("未知编码应返回空数组，实际 %s", string(api.Data))
	}
}

// TestIntegrationAreaTreeSemantics 树接口：空 parentCode → 全量嵌套树
// （顶层省级）；parentCode 指定 → 以其子级为根的子树。
func TestIntegrationAreaTreeSemantics(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	type node struct {
		Code     string  `json:"code"`
		Name     string  `json:"name"`
		Level    int     `json:"level"`
		Children []*node `json:"children"`
	}

	status, api := env.Request(t, http.MethodGet, "/system/area/tree", token, nil)
	areaMustOK(t, "全量树", status, api)
	var tree []*node
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析全量树: %v", err)
	}
	if len(tree) != 34 {
		t.Fatalf("全量树顶层应为 34 个省级节点，实际 %d", len(tree))
	}
	var hebei *node
	for _, n := range tree {
		if n.Code == "130000" {
			hebei = n
		}
	}
	if hebei == nil || len(hebei.Children) != 11 {
		t.Fatalf("全量树中河北省应有 11 个市级子节点")
	}

	// parentCode=130000 → 子树根为 11 个市（无孙级，区县未导入）。
	status, api = env.Request(t, http.MethodGet, "/system/area/tree?parentCode=130000", token, nil)
	areaMustOK(t, "河北省子树", status, api)
	var subtree []*node
	if err := json.Unmarshal(api.Data, &subtree); err != nil {
		t.Fatalf("解析子树: %v", err)
	}
	if len(subtree) != 11 || subtree[0].Code != "130100" {
		t.Fatalf("河北省子树应为 11 个市级节点且首个为 130100，实际 len=%d", len(subtree))
	}
}

// TestIntegrationAreaImport 导入链路：父子校验（库里已有/同批出现）、
// 按 code upsert、重复导入不重复、≤5000、非平台租户 403。
func TestIntegrationAreaImport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 1. 区县导入：父节点（130100 石家庄市）已在种子中 → 成功。
	status, api := env.Request(t, http.MethodPost, "/system/area/import", token, []areaItem{
		{Code: "130102", ParentCode: "130100", Name: "长安区", Level: 3},
		{Code: "130104", ParentCode: "130100", Name: "桥西区", Level: 3},
	})
	areaMustOK(t, "导入石家庄市区县", status, api)
	var imported struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(api.Data, &imported); err != nil || imported.Count != 2 {
		t.Fatalf("导入计数应为 2: %v（data=%s）", err, string(api.Data))
	}

	// 2. 树接口能看到导入的区县（130100 的子树含 130102）。
	status, api = env.Request(t, http.MethodGet, "/system/area/tree?parentCode=130100", token, nil)
	areaMustOK(t, "石家庄子树", status, api)
	if !strings.Contains(string(api.Data), "130102") || !strings.Contains(string(api.Data), "长安区") {
		t.Fatalf("石家庄子树应包含导入的区县，实际 %s", string(api.Data))
	}

	// 3. 父节点缺失 → 400。
	status, api = env.Request(t, http.MethodPost, "/system/area/import", token, []areaItem{
		{Code: "999102", ParentCode: "999100", Name: "幽灵区", Level: 3},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("父节点缺失应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 4. 同批出现 → 成功（省在市之后出现也允许）。
	status, api = env.Request(t, http.MethodPost, "/system/area/import", token, []areaItem{
		{Code: "888800", ParentCode: "888000", Name: "测试市", Level: 2},
		{Code: "888000", ParentCode: "", Name: "测试省", Level: 1},
	})
	areaMustOK(t, "同批父子导入", status, api)

	// 5. upsert：同 code 改名再导 → 行数不变、名称更新。
	status, api = env.Request(t, http.MethodPost, "/system/area/import", token, []areaItem{
		{Code: "130102", ParentCode: "130100", Name: "长安区（改）", Level: 3},
	})
	areaMustOK(t, "重复导入 upsert", status, api)
	var count int64
	if err := env.DB.Table("system_area").Where("code = ?", "130102").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("upsert 后 130102 应仍为 1 行（实际 %d，err=%v）", count, err)
	}
	status, api = env.Request(t, http.MethodGet, "/system/area/children?parentCode=130100", token, nil)
	areaMustOK(t, "石家庄子级", status, api)
	if !strings.Contains(string(api.Data), "长安区（改）") {
		t.Fatalf("upsert 应更新名称，实际 %s", string(api.Data))
	}

	// 6. 全量重复导入不产生重复行。
	before := areaCount(t, env)
	status, api = env.Request(t, http.MethodPost, "/system/area/import", token, []areaItem{
		{Code: "130102", ParentCode: "130100", Name: "长安区", Level: 3},
		{Code: "130104", ParentCode: "130100", Name: "桥西区", Level: 3},
		{Code: "888000", ParentCode: "", Name: "测试省", Level: 1},
		{Code: "888800", ParentCode: "888000", Name: "测试市", Level: 2},
	})
	areaMustOK(t, "全量重复导入", status, api)
	if after := areaCount(t, env); after != before {
		t.Fatalf("重复导入不应新增行：%d → %d", before, after)
	}

	// 7. 非法条目：层级越界 / 缺编码 / 缺名称 / 一级带父编码 → 400。
	for i, bad := range [][]areaItem{
		{{Code: "130199", ParentCode: "130100", Name: "非法层级", Level: 9}},
		{{Code: "", ParentCode: "", Name: "缺编码", Level: 1}},
		{{Code: "130198", ParentCode: "130100", Name: "", Level: 3}},
		{{Code: "130197", ParentCode: "130100", Name: "一级不应有父", Level: 1}},
	} {
		status, api = env.Request(t, http.MethodPost, "/system/area/import", token, bad)
		if status != http.StatusBadRequest {
			t.Fatalf("非法条目 #%d 应 400，实际 HTTP %d code %d msg %q", i, status, api.Code, api.Msg)
		}
	}

	// 8. 超过 5000 条 → 400（不发实际大导入前的快速失败）。
	big := make([]areaItem, 5001)
	for i := range big {
		big[i] = areaItem{Code: fmt.Sprintf("9%05d", i), ParentCode: "", Name: "x", Level: 1}
	}
	status, api = env.Request(t, http.MethodPost, "/system/area/import", token, big)
	if status != http.StatusBadRequest {
		t.Fatalf("超过 5000 条应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 9. 非平台租户 → 403（即使持平台管理员路由也不可达，需先造租户管理员）。
	tenantID := createSystemTenant(t, env, token, "地区导入越权租户")
	tenantToken := loginTenantArea(t, env, tenantID)
	status, api = env.Request(t, http.MethodPost, "/system/area/import", tenantToken, []areaItem{
		{Code: "777000", ParentCode: "", Name: "越权省", Level: 1},
	})
	if status != http.StatusForbidden {
		t.Fatalf("非平台租户导入应 403，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 读取开放给所有登录用户（spec §4.8.1）。
	status, api = env.Request(t, http.MethodGet, "/system/area/children", tenantToken, nil)
	areaMustOK(t, "非平台租户读取", status, api)

	// 10. 平台租户内无 system:area:import 权限码的角色 → 403（权限码收口）；
	// 同一账号读取仍然放行（spec §4.8.1）。
	noImportRole := createRoleByAPI(t, env, token, map[string]any{
		"name": "地区无导入权限角色", "code": "area_no_import", "dataScope": 5, "menuIds": []int64{113},
	})
	createUserByAPI(t, env, token, "area_no_import", 102, []int64{noImportRole})
	noImport := env.Login(t, "area_no_import", "123456")
	status, api = env.Request(t, http.MethodPost, "/system/area/import", noImport, []areaItem{
		{Code: "776000", ParentCode: "", Name: "无权省", Level: 1},
	})
	if status != http.StatusForbidden {
		t.Fatalf("无导入权限码应 403，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/area/children", noImport, nil)
	areaMustOK(t, "无权限码账号读取", status, api)
}

// createSystemTenant 创建租户并回读 ID（新租户内置 admin/123456）。
func createSystemTenant(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	areaMustOK(t, "创建租户 "+name, status, api)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10&name="+name, token, nil)
	areaMustOK(t, "查询租户 "+name, status, api)
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("租户 %s 应唯一命中: %v（data=%s）", name, err, string(api.Data))
	}
	return int64(page.List[0]["id"].(float64))
}

// loginTenantArea 以指定租户登录。
func loginTenantArea(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	areaMustOK(t, fmt.Sprintf("登录租户 %d", tenantID), status, api)
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}
