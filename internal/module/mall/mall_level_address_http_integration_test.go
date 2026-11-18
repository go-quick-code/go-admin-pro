//go:build integration

// mall 等级与地址域 HTTP 集成测试（issue #174，docs/mall-mvp-spec.md §3.1/§4）：
//   - 等级种子行「普通会员 level=1 experience=0」在场（seed 断言的 HTTP 侧镜像）；
//   - 等级 CRUD + name/level 双租户内唯一 409 + 更新撞唯一 409；
//   - 删除守卫：被会员引用 409，未引用可删；
//   - 等级清单（/mall/level/list）只含启用等级（挂 mall:member:query）；
//   - 按会员查地址：默认地址置顶、分页过滤、跨会员不可见（地址只读——
//     T1 无写入端点，用例数据经 tenant.Skip 直插，照 oa 集成测试先例）。
package mall_test

import (
	"encoding/json"
	"net/http"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// mallCreateLevel 创建等级（断言成功返回 ID）。
func mallCreateLevel(t *testing.T, env *testutil.IntegrationEnv, token, name string, levelValue int) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/level", token, map[string]any{
		"name": name, "level": levelValue,
	})
	mustOK(t, "创建等级 "+name, status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析等级响应: %v（data=%s）", err, api.Data)
	}
	return row.ID
}

// TestIntegrationLevelSeedRow 等级种子行：普通会员 level=1 experience=0。
func TestIntegrationLevelSeedRow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/mall/level/page?pageNo=1&pageSize=50", token, nil)
	mustOK(t, "等级分页", status, api)
	list := mallPageList(t, api)
	found := false
	for _, row := range list {
		if row["name"] == "普通会员" {
			found = true
			if row["level"].(float64) != 1 || row["experience"].(float64) != 0 {
				t.Fatalf("种子等级值不符：%s", row)
			}
		}
	}
	if !found {
		t.Fatalf("等级种子行「普通会员」缺失：%v", list)
	}
}

// TestIntegrationLevelCRUDUnique 等级 CRUD + name/level 双唯一 + 删除守卫。
func TestIntegrationLevelCRUDUnique(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 种子行已占 name=普通会员 / level=1：撞名 409。
	status, api := env.Request(t, http.MethodPost, "/mall/level", token, map[string]any{
		"name": "普通会员", "level": 5,
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("撞等级名：HTTP %d code %d，期望 409", status, api.Code)
	}
	// 撞 level 值 409。
	status, api = env.Request(t, http.MethodPost, "/mall/level", token, map[string]any{
		"name": "白银会员", "level": 1,
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("撞等级值：HTTP %d code %d，期望 409", status, api.Code)
	}

	silver := mallCreateLevel(t, env, token, "白银会员", 2)
	gold := mallCreateLevel(t, env, token, "黄金会员", 3)

	// 更新撞既有 name/level（改白银为黄金的名字）409。
	status, api = env.Request(t, http.MethodPut, "/mall/level", token, map[string]any{
		"id": silver, "name": "黄金会员", "level": 2,
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("更新撞等级名：HTTP %d code %d，期望 409", status, api.Code)
	}

	// 删除守卫：被会员引用 409（先建会员挂黄金等级）。
	mallCreateMember(t, env, token, map[string]any{"mobile": "13800000501", "levelId": gold})
	status, api = env.Request(t, http.MethodDelete, "/mall/level/"+itoa(gold), token, nil)
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("被引用等级删除：HTTP %d code %d，期望 409", status, api.Code)
	}

	// 未引用等级可删；删除后清单不再包含。
	status, api = env.Request(t, http.MethodDelete, "/mall/level/"+itoa(silver), token, nil)
	mustOK(t, "删除未引用等级", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/level/page?pageNo=1&pageSize=50", token, nil)
	mustOK(t, "删除后分页", status, api)
	for _, row := range mallPageList(t, api) {
		if int64(row["id"].(float64)) == silver {
			t.Fatalf("等级 %d 已删除，不应出现在分页", silver)
		}
	}

	// 清单端点（/mall/level/list）：只含启用等级。先停用黄金再验证。
	status, api = env.Request(t, http.MethodPut, "/mall/level", token, map[string]any{
		"id": gold, "name": "黄金会员", "level": 3, "status": 1,
	})
	mustOK(t, "停用黄金等级", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/level/list", token, nil)
	mustOK(t, "等级清单", status, api)
	var options []map[string]any
	if err := json.Unmarshal(api.Data, &options); err != nil {
		t.Fatalf("解析等级清单: %v（data=%s）", err, api.Data)
	}
	for _, row := range options {
		if int64(row["id"].(float64)) == gold {
			t.Fatalf("等级清单应只含启用等级，停用的黄金会员不应在场：%v", options)
		}
	}
}

// TestIntegrationAddressListByUser 按会员查地址：默认地址置顶、分页过滤、
// 未知会员 404（地址只读，数据直插）。
func TestIntegrationAddressListByUser(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	created := mallCreateMember(t, env, token, map[string]any{"mobile": "13800000601"})
	userID := int64(created["id"].(float64))
	other := mallCreateMember(t, env, token, map[string]any{"mobile": "13800000602"})
	otherID := int64(other["id"].(float64))

	// 直插地址（T1 无写入端点，照 oa 集成测试 tenant.Skip 直插先例）。
	addresses := []mallmodel.MemberAddress{
		{TenantID: 1, UserID: userID, ReceiverName: "张三", ReceiverMobile: "13800000601",
			Province: "浙江省", City: "杭州市", District: "西湖区", Detail: "文一西路 100 号", IsDefault: 0},
		{TenantID: 1, UserID: userID, ReceiverName: "张三 backup", ReceiverMobile: "13800000601",
			Province: "浙江省", City: "杭州市", District: "余杭区", Detail: "文一西路 200 号", IsDefault: 1},
		{TenantID: 1, UserID: otherID, ReceiverName: "李四", ReceiverMobile: "13800000602",
			Province: "上海市", City: "上海市", District: "浦东新区", Detail: "世纪大道 1 号", IsDefault: 0},
	}
	if err := tenant.Skip(env.DB).Create(&addresses).Error; err != nil {
		t.Fatalf("直插地址数据: %v", err)
	}

	// 会员详情带出地址（默认地址置顶）。
	status, api := env.Request(t, http.MethodGet, detailPath(userID), token, nil)
	mustOK(t, "会员详情（含地址）", status, api)
	detail := parseJSON(t, string(api.Data))
	list, ok := detail["addresses"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("会员详情应带 2 条地址：%s", api.Data)
	}
	first := list[0].(map[string]any)
	if first["isDefault"].(float64) != 1 {
		t.Fatalf("默认地址应置顶：%s", list)
	}

	// 按会员端点。
	status, api = env.Request(t, http.MethodGet, "/mall/address/member/"+itoa(userID), token, nil)
	mustOK(t, "按会员查地址", status, api)
	var userAddresses []map[string]any
	if err := json.Unmarshal(api.Data, &userAddresses); err != nil {
		t.Fatalf("解析按会员地址: %v（data=%s）", err, api.Data)
	}
	if len(userAddresses) != 2 || userAddresses[0]["isDefault"].(float64) != 1 {
		t.Fatalf("按会员地址应 2 条且默认置顶：%s", api.Data)
	}

	// 分页端点按 receiver 过滤。
	status, api = env.Request(t, http.MethodGet, "/mall/address/page?receiver=李四&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "地址分页（收货人过滤）", status, api)
	filtered := mallPageList(t, api)
	if len(filtered) != 1 || filtered[0]["receiverName"] != "李四" {
		t.Fatalf("收货人过滤应唯一命中李四：%v", filtered)
	}

	// 未知会员：404。
	status, api = env.Request(t, http.MethodGet, "/mall/address/member/999999", token, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("未知会员查地址：HTTP %d code %d，期望 404", status, api.Code)
	}
}
