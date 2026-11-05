//go:build integration

// PR #39 独立审查修复的 HTTP 集成用例（P2）：
//   - 作废合同不能新增回款计划/记录；
//   - 公海客户详情与负责人历史对持 crm:customer:pool:view 的用户可读，
//     无码仍 404。
//
// 与既有用例共用真实 router + 生产中间件 + 真实 MySQL（-p 1 串行跑包）。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationReceivableVoidContractRejected P2：作废合同不能新增回款计划或
// 登记回款记录（计划可先建再作废；作废后两条创建路径都返回中文 400）。
func TestIntegrationReceivableVoidContractRejected(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "PR39 作废回款客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "PR39 作废回款合同", map[string]any{
		"customerId": customerID, "amount": "100.00",
	})
	contractID := int64(contract["id"].(float64))
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "50.00", "dueDate": "2030-01-01",
	})
	planID := int64(plan["id"].(float64))

	status, api := changeContractStatus(t, env, admin, contractID, 4, "")
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("作废合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	status, api = env.Request(t, http.MethodPost, "/crm/receivable/plan", admin, map[string]any{
		"contractId": contractID, "periodNo": 2, "amount": "10.00", "dueDate": "2030-01-01",
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "作废") {
		t.Fatalf("作废合同新增计划：HTTP %d code %d msg %q，期望 400 且文案含「作废」", status, api.Code, api.Msg)
	}

	status, api = env.Request(t, http.MethodPost, "/crm/receivable/record", admin, map[string]any{
		"planId": planID, "amount": "10.00", "receiptDate": time.Now().Format("2006-01-02"),
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "作废") {
		t.Fatalf("作废合同登记记录：HTTP %d code %d msg %q，期望 400 且文案含「作废」", status, api.Code, api.Msg)
	}
}

// TestIntegrationPoolCustomerReadWithPoolView P2：公海客户详情与负责人历史
// 对持 crm:customer:pool:view 的用户可读（不受数据范围过滤）；无 pool:view 时
// 仍按数据范围 404（不泄露存在性）。
func TestIntegrationPoolCustomerReadWithPoolView(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	c := createCustomer(t, env, admin, "PR39 公海可读客户", nil)
	id := int64(c["id"].(float64))
	status, api := releaseCustomerRaw(t, env, admin, id, "PR39 公海可读")
	mustOK(t, "释放到公海", status, api)

	// 只有公海菜单（crm:customer:pool:view，数据范围仅本人）：详情与历史可读
	// ——路由接受 pool:view 单独放行，service 只放行 owner 为空的公海客户。
	poolRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "PR39 公海只读角色", "code": "pr39_pool_read", "dataScope": 5,
		"menuIds": []int64{poolCustomerMenuID},
	})
	createUserByAPI(t, env, admin, "pr39_pool_reader", 103, []int64{poolRole})
	reader := env.Login(t, "pr39_pool_reader", "123456")

	st, detailAPI := getCustomer(t, env, reader, id)
	mustOK(t, "有 pool:view 查公海详情", st, detailAPI)
	var detail map[string]any
	if err := json.Unmarshal(detailAPI.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(0) {
		t.Fatalf("公海客户详情 owner 应为空，实际 %v", detail["ownerUserId"])
	}
	rows, total := pageOwnerHistory(t, env, reader, id)
	if total < 2 || len(rows) < 2 {
		t.Fatalf("有 pool:view 应能看到负责人历史（create+release），实际 %d 行", total)
	}
	if rows[0]["action"] != "release" {
		t.Fatalf("历史最新一行应为 release，实际 %v", rows[0]["action"])
	}

	// 只有 query、无 pool:view（数据范围仅本人）：详情/历史 404
	// （路由放行 query，但公海客户不受数据范围过滤，只有 pool:view 才可见）。
	noPoolRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "PR39 无公海角色", "code": "pr39_pool_none", "dataScope": 5,
		"menuIds": []int64{crmCustomerMenuID},
	})
	createUserByAPI(t, env, admin, "pr39_pool_none", 103, []int64{noPoolRole})
	noPool := env.Login(t, "pr39_pool_none", "123456")
	if st, _ := getCustomer(t, env, noPool, id); st != http.StatusNotFound {
		t.Fatalf("无 pool:view 查公海详情：HTTP %d，期望 404", st)
	}
	st, api = env.Request(t, http.MethodGet,
		fmt.Sprintf("/crm/customer/%d/owner-history?pageNo=1&pageSize=10", id), noPool, nil)
	if st != http.StatusNotFound {
		t.Fatalf("无 pool:view 查公海历史：HTTP %d code %d msg %q，期望 404", st, api.Code, api.Msg)
	}
}
