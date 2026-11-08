//go:build integration

// 单据内核 adapter conformance（ADR 0018，01a tracer）：同一组 9 步生命周期
// 断言跑 stock-in / stock-out，锁住「内核重构前后 HTTP 行为逐字等价」。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

type docConformanceCase struct {
	name      string
	path      string // /erp/stock-in
	docType   string // stock-in
	noPattern *regexp.Regexp
	create    func(env *testutil.IntegrationEnv, warehouseID, productID int64) map[string]any
	update    func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64) map[string]any
	seed      func(t *testing.T, env *testutil.IntegrationEnv, token string, warehouseID, productID int64)
}

func docConformanceCases() []docConformanceCase {
	return []docConformanceCase{
		{
			name:      "stock-in",
			path:      "/erp/stock-in",
			docType:   "stock-in",
			noPattern: stockInNoRe,
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64) map[string]any {
				return map[string]any{
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 10:00:00",
					"type":        50,
					"remark":      "conformance",
					"items":       []map[string]any{stockItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64) map[string]any {
				return map[string]any{
					"id":          docID,
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 11:00:00",
					"type":        99,
					"remark":      "conformance-updated",
					"items":       []map[string]any{stockItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "stock-out",
			path:      "/erp/stock-out",
			docType:   "stock-out",
			noPattern: stockOutNoRe,
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64) map[string]any {
				return map[string]any{
					"warehouseId": warehouseID,
					"outTime":     env.Date() + " 10:00:00",
					"type":        99,
					"remark":      "conformance",
					"items":       []map[string]any{stockItemJSON(productID, "2", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64) map[string]any {
				return map[string]any{
					"id":          docID,
					"warehouseId": warehouseID,
					"outTime":     env.Date() + " 11:00:00",
					"type":        99,
					"remark":      "conformance-updated",
					"items":       []map[string]any{stockItemJSON(productID, "2", "3.00")},
				}
			},
			seed: func(t *testing.T, env *testutil.IntegrationEnv, token string, warehouseID, productID int64) {
				t.Helper()
				in := createStockInDoc(t, env, token, map[string]any{
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 09:00:00",
					"type":        99,
					"items":       []map[string]any{stockItemJSON(productID, "10", "1.00")},
				})
				if status, api := approveDoc(t, env, token, "stock-in", int64(in["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
					t.Fatalf("seed 入库审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
				}
			},
		},
	}
}

func createConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, warehouseID, productID int64) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, tc.path, token, tc.create(env, warehouseID, productID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

func updateConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, docID, warehouseID, productID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, tc.path, token, tc.update(env, docID, warehouseID, productID))
}

func deleteConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, docID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, fmt.Sprintf("%s/%d", tc.path, docID), token, nil)
}

func getConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, docID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("%s/%d", tc.path, docID), token, nil)
}

// TestIntegrationDocConformance 同一组生命周期断言跑所有已接入内核的单据类型。
func TestIntegrationDocConformance(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	for _, tc := range docConformanceCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			warehouseID, productID := setupStockEnv(t, env, token, "一致性"+tc.name)
			if tc.seed != nil {
				tc.seed(t, env, token, warehouseID, productID)
			}

			// 1. create：未审核、单号格式。
			created := createConformanceDoc(t, env, token, tc, warehouseID, productID)
			docID := int64(created["id"].(float64))
			if created["status"] != float64(10) {
				t.Fatalf("新建状态 = %v，期望 10", created["status"])
			}
			if !tc.noPattern.MatchString(fmt.Sprint(created["no"])) {
				t.Fatalf("单号 %v 不符 %s", created["no"], tc.noPattern)
			}

			// 2. get：详情可读。
			if status, api := getConformanceDoc(t, env, token, tc, docID); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 3. update（未审核）：成功。
			if status, api := updateConformanceDoc(t, env, token, tc, docID, warehouseID, productID); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("未审核编辑：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 4. approve：成功。
			if status, api := approveDoc(t, env, token, tc.docType, docID, 20); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 5. update（已审核）：409。
			if status, api := updateConformanceDoc(t, env, token, tc, docID, warehouseID, productID); status != http.StatusConflict || !strings.Contains(api.Msg, "已审核单据不可修改") {
				t.Fatalf("已审核编辑：HTTP %d msg %q，期望 409/已审核单据不可修改", status, api.Msg)
			}

			// 6. delete（已审核）：409。
			if status, api := deleteConformanceDoc(t, env, token, tc, docID); status != http.StatusConflict || !strings.Contains(api.Msg, "已审核单据不可删除") {
				t.Fatalf("已审核删除：HTTP %d msg %q，期望 409/已审核单据不可删除", status, api.Msg)
			}

			// 7. unapprove：成功。
			if status, api := approveDoc(t, env, token, tc.docType, docID, 10); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("反审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 8. update（反审核后）：成功。
			if status, api := updateConformanceDoc(t, env, token, tc, docID, warehouseID, productID); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("反审核后编辑：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 9. delete（未审核）：成功，随后详情 404。
			if status, api := deleteConformanceDoc(t, env, token, tc, docID); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("未审核删除：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}
			if status, api := getConformanceDoc(t, env, token, tc, docID); status != http.StatusNotFound {
				t.Fatalf("删除后详情：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
			}

			// 10. 幂等目标态：重复审核 200 且状态保持 20。
			idem := createConformanceDoc(t, env, token, tc, warehouseID, productID)
			idemID := int64(idem["id"].(float64))
			for i := 0; i < 2; i++ {
				if status, api := approveDoc(t, env, token, tc.docType, idemID, 20); status != http.StatusOK || api.Code != 0 {
					t.Fatalf("第 %d 次审核：HTTP %d code %d msg %q", i+1, status, api.Code, api.Msg)
				}
			}
			status, api := getConformanceDoc(t, env, token, tc, idemID)
			if status != http.StatusOK || api.Code != 0 {
				t.Fatalf("幂等审核后详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}
			var got map[string]any
			if err := json.Unmarshal(api.Data, &got); err != nil {
				t.Fatalf("解析幂等详情: %v（data=%s）", err, string(api.Data))
			}
			if got["status"] != float64(20) {
				t.Fatalf("幂等审核后状态 = %v，期望 20", got["status"])
			}
		})
	}
}
