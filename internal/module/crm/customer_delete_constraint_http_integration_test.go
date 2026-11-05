//go:build integration

// 客户删除约束 HTTP 集成测试（spec §4.5）：客户存在未作废合同时禁止删除。
// 「未作废」= status <> 4 且合同未软删；草稿/待审/已签都算未作废，
// 作废或软删合同后客户恢复可删。
package crm_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// deleteCustomer 删除客户并返回 HTTP 状态与业务响应。
func deleteCustomer(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/customer/%d", id), token, nil)
}

// TestIntegrationCustomerDeleteConstraint 未作废合同阻止删除客户：
// 草稿/待审/已签合同逐级验证 400，合同作废后删除恢复；软删合同同样不再算未作废。
func TestIntegrationCustomerDeleteConstraint(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "删除约束客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "删除约束合同", map[string]any{
		"customerId": customerID, "amount": "1000.00",
	})
	contractID := int64(contract["id"].(float64))

	// 草稿合同（status=1）已属「未作废」：删除返回 400 与可读中文提示。
	status, api := deleteCustomer(t, env, admin, customerID)
	if status != http.StatusBadRequest {
		t.Fatalf("客户存在草稿合同：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if api.Msg == "" {
		t.Fatal("客户存在草稿合同：应返回中文错误信息")
	}

	// 待审、已签同样阻止删除。
	if sc, a := changeContractStatus(t, env, admin, contractID, 2, ""); sc != http.StatusOK || a.Code != 0 {
		t.Fatalf("提交合同审批：HTTP %d code %d msg %q", sc, a.Code, a.Msg)
	}
	if sc, a := deleteCustomer(t, env, admin, customerID); sc != http.StatusBadRequest {
		t.Fatalf("客户存在待审合同：HTTP %d code %d msg %q，期望 400", sc, a.Code, a.Msg)
	}
	if sc, a := changeContractStatus(t, env, admin, contractID, 3, ""); sc != http.StatusOK || a.Code != 0 {
		t.Fatalf("审批通过合同：HTTP %d code %d msg %q", sc, a.Code, a.Msg)
	}
	if sc, a := deleteCustomer(t, env, admin, customerID); sc != http.StatusBadRequest {
		t.Fatalf("客户存在已签合同：HTTP %d code %d msg %q，期望 400", sc, a.Code, a.Msg)
	}

	// 合同作废后不再算「未作废」，客户恢复可删。
	if sc, a := changeContractStatus(t, env, admin, contractID, 4, ""); sc != http.StatusOK || a.Code != 0 {
		t.Fatalf("作废合同：HTTP %d code %d msg %q", sc, a.Code, a.Msg)
	}
	if sc, a := deleteCustomer(t, env, admin, customerID); sc != http.StatusOK || a.Code != 0 {
		t.Fatalf("合同作废后删除客户：HTTP %d code %d msg %q，期望 200", sc, a.Code, a.Msg)
	}
	if sc, _ := getCustomer(t, env, admin, customerID); sc != http.StatusNotFound {
		t.Fatalf("删除客户后详情：HTTP %d，期望 404", sc)
	}
}

// TestIntegrationCustomerDeleteConstraintSoftDeletedContract 软删除的合同不算
// 「未作废」：草稿合同软删后，客户可以删除（spec §4.5 的未软删口径）。
func TestIntegrationCustomerDeleteConstraintSoftDeletedContract(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "软删合同客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "软删合同", map[string]any{
		"customerId": customerID, "amount": "500.00",
	})
	contractID := int64(contract["id"].(float64))

	if sc, a := deleteCustomer(t, env, admin, customerID); sc != http.StatusBadRequest {
		t.Fatalf("草稿合同存在时删除客户：HTTP %d code %d msg %q，期望 400", sc, a.Code, a.Msg)
	}

	// 草稿合同可删除；删除后客户不再被约束。
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", contractID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除草稿合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if sc, a := deleteCustomer(t, env, admin, customerID); sc != http.StatusOK || a.Code != 0 {
		t.Fatalf("合同软删后删除客户：HTTP %d code %d msg %q，期望 200", sc, a.Code, a.Msg)
	}
}
