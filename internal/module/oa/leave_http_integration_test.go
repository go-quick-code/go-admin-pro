//go:build integration

// 请假类型与额度的 HTTP 集成用例（issue #51，docs/oa-mvp-spec.md §3.5/§4.8/
// §4.14）：类型 CRUD/启停/删除约束、额度初始化幂等、调整留痕与原因必填、
// 员工本人可见、oa:leave:manage 可管全租户、跨租户 404、权限码逐码。
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t51，-p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---- 测试基建 ----

// leaveMenuID / 权限码：菜单 522 请假类型与额度，管理码 oa:leave:manage 挂菜单
// （本工单无独立按钮码，见 seed.go 注释）。
const (
	leaveMenuID     = int64(522)
	leaveMineMenuID = int64(528)
)

// createLeaveType 创建请假类型，返回 (HTTP 状态, 类型 ID, msg)。
func createLeaveType(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, int64, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/leave/type", token, body)
	if api.Code != 0 {
		return status, 0, api.Msg
	}
	var data struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析创建类型响应: %v（data=%s）", err, string(api.Data))
	}
	return status, data.ID, ""
}

// listLeaveTypes 类型列表，返回 (HTTP 状态, 行数组, msg)。
func listLeaveTypes(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, []map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/leave/type/list"+query, token, nil)
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var rows []map[string]any
	if err := json.Unmarshal(api.Data, &rows); err != nil {
		t.Fatalf("解析类型列表: %v（data=%s）", err, string(api.Data))
	}
	return status, rows, ""
}

// leaveTypeOf 从列表里按编码取类型行（不存在返回 nil）。
func leaveTypeOf(rows []map[string]any, code string) map[string]any {
	for _, row := range rows {
		if row["code"] == code {
			return row
		}
	}
	return nil
}

// balancePage 额度管理分页，返回 (HTTP 状态, 行数组, total, msg)。
func balancePage(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, []map[string]any, int64, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/leave/balance/page"+query, token, nil)
	if api.Code != 0 {
		return status, nil, 0, api.Msg
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析额度分页: %v（data=%s）", err, string(api.Data))
	}
	return status, page.List, page.Total, ""
}

// myBalances 本人额度，返回 (HTTP 状态, 响应, msg)。
func myBalances(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/leave/balance/mine"+query, token, nil)
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析我的额度: %v（data=%s）", err, string(api.Data))
	}
	return status, data, ""
}

// mineItemOf 从我的额度响应里按类型编码取行。
func mineItemOf(data map[string]any, code string) map[string]any {
	items, _ := data["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["typeCode"] == code {
			return item
		}
	}
	return nil
}

// initBalances 额度初始化，返回 (HTTP 状态, created, total, msg)。
func initBalances(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, int, int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/leave/balance/init", token, body)
	if api.Code != 0 {
		return status, 0, 0, api.Msg
	}
	var data struct {
		Created int `json:"created"`
		Total   int `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析初始化响应: %v（data=%s）", err, string(api.Data))
	}
	return status, data.Created, data.Total, ""
}

// adjustBalance 管理员调整额度，返回 (HTTP 状态, 行, msg)。
func adjustBalance(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/leave/balance/adjust", token, body)
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析调整响应: %v（data=%s）", err, string(api.Data))
	}
	return status, data, ""
}

// setupLeaveTypes 建一对常用类型：annual（占额度 5 天）与 personal（不占额度），
// 返回 (annualID, personalID)。
func setupLeaveTypes(t *testing.T, env *testutil.IntegrationEnv, admin string) (int64, int64) {
	t.Helper()
	status, annualID, msg := createLeaveType(t, env, admin, map[string]any{
		"name": "年假", "code": "annual", "balanceManaged": true, "annualQuota": "5",
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建年假类型：HTTP %d msg %q", status, msg)
	}
	status, personalID, msg := createLeaveType(t, env, admin, map[string]any{
		"name": "事假", "code": "personal", "balanceManaged": false,
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建事假类型：HTTP %d msg %q", status, msg)
	}
	return annualID, personalID
}

// ---- 类型管理 ----

// TestIntegrationLeaveTypeCRUD 类型全链路：创建默认启用、列表（登录即可、
// sort 升序）、更新（名称/额度/顺序）、唯一约束 409、非法参数 400、更新
// 不存在 404；编码创建后不可变（更新请求不携带 code）。
func TestIntegrationLeaveTypeCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	_, personalID := setupLeaveTypes(t, env, admin)

	t.Run("列表_登录即可_默认启用", func(t *testing.T) {
		// 登录用户（管理员）可见；行按 sort/id 升序（annual sort 0 → personal sort 1）。
		status, rows, msg := listLeaveTypes(t, env, admin, "")
		if status != http.StatusOK || msg != "" || len(rows) != 2 {
			t.Fatalf("类型列表：HTTP %d 行数 %d msg %q", status, len(rows), msg)
		}
		if rows[0]["code"] != "annual" || rows[1]["code"] != "personal" {
			t.Fatalf("列表顺序应为 annual 在前（sort 升序），实际 %v / %v", rows[0]["code"], rows[1]["code"])
		}
		annual := rows[0]
		if annual["balanceManaged"] != true || annual["annualQuota"] != "5.00" || annual["status"] != float64(0) {
			t.Fatalf("annual 字段不符: %+v", annual)
		}
	})

	t.Run("员工登录查列表_无需管理码", func(t *testing.T) {
		createUser(t, env, admin, "leave_emp", 101) // 无角色的启用用户
		token := env.Login(t, "leave_emp", "123456")
		if status, _, msg := listLeaveTypes(t, env, token, ""); status != http.StatusOK || msg != "" {
			t.Fatalf("员工查类型列表应登录即可：HTTP %d msg %q", status, msg)
		}
	})

	t.Run("唯一约束_409", func(t *testing.T) {
		if status, _, msg := createLeaveType(t, env, admin, map[string]any{
			"name": "年假复制", "code": "annual", "balanceManaged": true, "annualQuota": "3",
		}); status != http.StatusConflict || msg == "" {
			t.Fatalf("重复编码应 409，实际 HTTP %d msg %q", status, msg)
		}
		if status, _, msg := createLeaveType(t, env, admin, map[string]any{
			"name": "事假", "code": "personal-2", "balanceManaged": false,
		}); status != http.StatusConflict || msg == "" {
			t.Fatalf("重复名称应 409，实际 HTTP %d msg %q", status, msg)
		}
	})

	t.Run("非法参数_400", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
		}{
			{"名称为空", map[string]any{"name": "", "code": "ok-code"}},
			{"名称超长", map[string]any{"name": string(make([]rune, 51)), "code": "ok-code"}},
			{"编码为空", map[string]any{"name": "合法", "code": ""}},
			{"编码大写", map[string]any{"name": "合法", "code": "Annual"}},
			{"编码含下划线", map[string]any{"name": "合法", "code": "bad_code"}},
			{"编码连字符开头", map[string]any{"name": "合法", "code": "-bad"}},
			{"额度负数", map[string]any{"name": "合法", "code": "ok-code", "balanceManaged": true, "annualQuota": "-1"}},
			{"额度两位小数", map[string]any{"name": "合法", "code": "ok-code", "balanceManaged": true, "annualQuota": "1.25"}},
			{"额度超范围", map[string]any{"name": "合法", "code": "ok-code", "balanceManaged": true, "annualQuota": "10000"}},
			{"不占额度却配额度", map[string]any{"name": "合法", "code": "ok-code", "balanceManaged": false, "annualQuota": "3"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, _, msg := createLeaveType(t, env, admin, tc.body)
				if status != http.StatusBadRequest || msg == "" {
					t.Fatalf("%s 应 400，实际 HTTP %d msg %q", tc.name, status, msg)
				}
			})
		}
	})

	t.Run("更新_重命名_额度", func(t *testing.T) {
		status, api := env.Request(t, http.MethodPut, "/oa/leave/type", admin, map[string]any{
			"id": personalID, "name": "事假（无薪）", "balanceManaged": false, "sort": 9, "remark": "无薪事假",
		})
		mustOK(t, "更新类型", status, api)
		_, rows, _ := listLeaveTypes(t, env, admin, "")
		row := leaveTypeOf(rows, "personal")
		if row["name"] != "事假（无薪）" || row["sort"] != float64(9) || row["remark"] != "无薪事假" {
			t.Fatalf("更新未生效: %+v", row)
		}
		// 更新与其他类型重名 → 409。
		status, api = env.Request(t, http.MethodPut, "/oa/leave/type", admin, map[string]any{
			"id": personalID, "name": "年假", "balanceManaged": false,
		})
		if status != http.StatusConflict {
			t.Fatalf("更新为重复名称应 409，实际 HTTP %d msg %q", status, api.Msg)
		}
		// 更新不存在的类型 → 404。
		status, api = env.Request(t, http.MethodPut, "/oa/leave/type", admin, map[string]any{
			"id": 999999, "name": "幽灵", "balanceManaged": false,
		})
		if status != http.StatusNotFound {
			t.Fatalf("更新不存在类型应 404，实际 HTTP %d", status)
		}
	})
}

// TestIntegrationLeaveTypeStatus 启停：停用后 status=1；status 列表过滤；
// 非法 status 400；启停不存在的类型 404。
func TestIntegrationLeaveTypeStatus(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, _ := setupLeaveTypes(t, env, admin)

	if status, _ := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/leave/type/%d/status", annualID), admin, map[string]any{"status": 1}); status != http.StatusOK {
		t.Fatalf("停用：HTTP %d", status)
	}
	_, rows, _ := listLeaveTypes(t, env, admin, "")
	if row := leaveTypeOf(rows, "annual"); row["status"] != float64(1) {
		t.Fatalf("停用后 status 应为 1: %+v", row)
	}
	// status=1 过滤：只见停用行。
	if _, rows, _ = listLeaveTypes(t, env, admin, "?status=1"); len(rows) != 1 || rows[0]["code"] != "annual" {
		t.Fatalf("status=1 过滤应只见 annual，实际 %v", rows)
	}
	if _, rows, _ = listLeaveTypes(t, env, admin, "?status=0"); len(rows) != 1 || rows[0]["code"] != "personal" {
		t.Fatalf("status=0 过滤应只见 personal，实际 %v", rows)
	}
	// 重新启用。
	if status, _ := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/leave/type/%d/status", annualID), admin, map[string]any{"status": 0}); status != http.StatusOK {
		t.Fatalf("启用：HTTP %d", status)
	}
	_, rows, _ = listLeaveTypes(t, env, admin, "?status=0")
	if len(rows) != 2 {
		t.Fatalf("启用后 status=0 应 2 行，实际 %d", len(rows))
	}
	// 非法 status 与不存在类型。
	if status, _ := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/leave/type/%d/status", annualID), admin, map[string]any{"status": 2}); status != http.StatusBadRequest {
		t.Fatalf("status=2 应 400，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodPut, "/oa/leave/type/999999/status", admin, map[string]any{"status": 1}); status != http.StatusNotFound {
		t.Fatalf("启停不存在类型应 404，实际 HTTP %d", status)
	}
}

// TestIntegrationLeaveTypeDeleteConstraint 删除约束（spec §4.14）：未被引用
// 可删；被申请引用（oa_leave_request 有行，任意状态）只能停用不能删除 409；
// 删除不存在 404；软删行仍占用编码（重建同码 409）。
func TestIntegrationLeaveTypeDeleteConstraint(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, _ := setupLeaveTypes(t, env, admin)

	// 未引用类型可删。
	status, freeID, msg := createLeaveType(t, env, admin, map[string]any{
		"name": "临时类型", "code": "temp", "balanceManaged": false,
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建临时类型：HTTP %d msg %q", status, msg)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/leave/type/%d", freeID), admin, nil); status != http.StatusOK {
		t.Fatalf("删除未引用类型：HTTP %d", status)
	}
	if _, rows, _ := listLeaveTypes(t, env, admin, ""); leaveTypeOf(rows, "temp") != nil {
		t.Fatal("删除后列表不应再出现 temp")
	}

	// 直插一条申请引用（写入路径由 #52 提供，这里只为验证删除约束）。
	if err := env.DB.Exec(
		"INSERT INTO `oa_leave_request` (`tenant_id`, `user_id`, `leave_type_id`, `start_date`, `start_half`, "+
			"`end_date`, `end_half`, `duration_days`, `reason`, `status`) "+
			"VALUES (1, 2, ?, '2026-12-01', 0, '2026-12-02', 1, 3.0, '测试引用', 0)", annualID,
	).Error; err != nil {
		t.Fatalf("直插引用申请: %v", err)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/leave/type/%d", annualID), admin, nil); status != http.StatusConflict || api.Msg == "" {
		t.Fatalf("被引用类型删除应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 被引用但可以停用。
	if status, _ := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/leave/type/%d/status", annualID), admin, map[string]any{"status": 1}); status != http.StatusOK {
		t.Fatalf("被引用类型停用：HTTP %d", status)
	}

	// 删除不存在 → 404。
	if status, _ := env.Request(t, http.MethodDelete, "/oa/leave/type/999999", admin, nil); status != http.StatusNotFound {
		t.Fatalf("删除不存在类型应 404，实际 HTTP %d", status)
	}

	// 软删行仍占用编码：重建同码 409。
	if status, _, msg := createLeaveType(t, env, admin, map[string]any{
		"name": "临时类型二", "code": "temp", "balanceManaged": false,
	}); status != http.StatusConflict || msg == "" {
		t.Fatalf("重建已删编码应 409（软删行占码），实际 HTTP %d msg %q", status, msg)
	}
}

// ---- 额度 ----

// TestIntegrationLeaveBalanceInitIdempotent 初始化幂等（验收：重复初始化不
// 重复建行、不覆盖已有行）：首次 created=total；重复 created=0；已有行（被
// 调整过）不被重置；显式 typeIds 与默认全量；非法参数 400。
func TestIntegrationLeaveBalanceInitIdempotent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, personalID := setupLeaveTypes(t, env, admin)
	u1 := createUser(t, env, admin, "init_u1", 101)
	u2 := createUser(t, env, admin, "init_u2", 101)

	t.Run("首次初始化", func(t *testing.T) {
		status, created, total, msg := initBalances(t, env, admin, map[string]any{
			"year": 2026, "userIds": []int64{u1, u2},
		})
		if status != http.StatusOK || msg != "" {
			t.Fatalf("初始化：HTTP %d msg %q", status, msg)
		}
		// 默认类型集 = 启用中的占用额度类型（仅 annual），组合数 = 2 人 × 1 类型。
		if created != 2 || total != 2 {
			t.Fatalf("首次初始化 created=%d total=%d，期望 2/2", created, total)
		}
		// 额度快照 = 类型年度额度 5。
		status, rows, pageTotal, _ := balancePage(t, env, admin, fmt.Sprintf("?userId=%d&year=2026", u1))
		if status != http.StatusOK || pageTotal != 1 || len(rows) != 1 {
			t.Fatalf("额度分页：HTTP %d total %d rows %v", status, pageTotal, rows)
		}
		if rows[0]["quota"] != "5.00" || rows[0]["adjustment"] != "0.00" || rows[0]["used"] != "0.00" || rows[0]["available"] != "5.00" {
			t.Fatalf("额度快照不符: %+v", rows[0])
		}
		if rows[0]["typeName"] != "年假" || rows[0]["userName"] == "" {
			t.Fatalf("额度行应附类型名与员工姓名: %+v", rows[0])
		}
	})

	t.Run("重复初始化幂等", func(t *testing.T) {
		status, created, total, _ := initBalances(t, env, admin, map[string]any{
			"year": 2026, "userIds": []int64{u1, u2},
		})
		if status != http.StatusOK || created != 0 || total != 2 {
			t.Fatalf("重复初始化 created=%d total=%d，期望 0/2（不重复建行）", created, total)
		}
	})

	t.Run("已有调整不被覆盖", func(t *testing.T) {
		// u1 调整 +2.5 天，再初始化：adjustment 不被重置。
		status, item, msg := adjustBalance(t, env, admin, map[string]any{
			"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "2.5", "reason": "上年度结转",
		})
		if status != http.StatusOK || msg != "" {
			t.Fatalf("调整：HTTP %d msg %q", status, msg)
		}
		if item["adjustment"] != "2.50" {
			t.Fatalf("调整后 adjustment 应 2.50: %+v", item)
		}
		if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 2026, "userIds": []int64{u1}}); status != http.StatusOK {
			t.Fatalf("重复初始化：HTTP %d", status)
		}
		_, rows, pageTotal, _ := balancePage(t, env, admin, fmt.Sprintf("?userId=%d&year=2026", u1))
		if pageTotal != 1 || len(rows) != 1 || rows[0]["adjustment"] != "2.50" {
			t.Fatalf("重复初始化不应覆盖调整: total %d rows %v", pageTotal, rows)
		}
	})

	t.Run("显式类型列表与非法参数", func(t *testing.T) {
		if status, _, _, msg := initBalances(t, env, admin, map[string]any{
			"year": 2026, "userIds": []int64{u1}, "typeIds": []int64{personalID},
		}); status != http.StatusBadRequest || msg == "" {
			t.Fatalf("不占额度类型初始化应 400，实际 HTTP %d msg %q", status, msg)
		}
		if status, _, _, _ := initBalances(t, env, admin, map[string]any{
			"year": 2026, "userIds": []int64{u1}, "typeIds": []int64{annualID, personalID},
		}); status != http.StatusBadRequest {
			t.Fatalf("含不占额度类型的显式列表应 400，实际 HTTP %d", status)
		}
		if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 2026, "userIds": []int64{}}); status != http.StatusBadRequest {
			t.Fatalf("空 userIds 应 400，实际 HTTP %d", status)
		}
		if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 1900, "userIds": []int64{u1}}); status != http.StatusBadRequest {
			t.Fatalf("非法年度应 400，实际 HTTP %d", status)
		}
		if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 2026, "userIds": []int64{999999}, "typeIds": []int64{999998}}); status != http.StatusNotFound {
			t.Fatalf("不存在的类型应 404，实际 HTTP %d", status)
		}
	})
}

// TestIntegrationLeaveBalanceAdjust 调整留痕与原因必填（spec §4.8）：原因必填
// （缺失/空白 400）；adjustment 累加可负；留痕 adjust_reason + updater（操作人）
// + updateTime；调整即初始化（缺行先按类型年度额度补行）；delta 校验。
func TestIntegrationLeaveBalanceAdjust(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, personalID := setupLeaveTypes(t, env, admin)
	u1 := createUser(t, env, admin, "adj_u1", 101)

	t.Run("缺行调整即初始化", func(t *testing.T) {
		// 没有初始化过：直接调整 +2.5 → 行自动补建（quota=5 快照）。
		status, item, msg := adjustBalance(t, env, admin, map[string]any{
			"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "2.5", "reason": "入职折算",
		})
		if status != http.StatusOK || msg != "" {
			t.Fatalf("调整：HTTP %d msg %q", status, msg)
		}
		if item["quota"] != "5.00" || item["adjustment"] != "2.50" || item["available"] != "7.50" {
			t.Fatalf("调整即初始化结果不符: %+v", item)
		}
		if item["adjustReason"] != "入职折算" || item["userId"] != float64(u1) || item["typeName"] != "年假" {
			t.Fatalf("调整留痕/归属不符: %+v", item)
		}
		if item["updateTime"] == "" {
			t.Fatalf("调整应留 updateTime: %+v", item)
		}
		// 操作人留痕：updater 记录管理员 ID（审计插件回填）。
		adminID := adminUserID(t, env, admin)
		if item["updater"] != float64(adminID) {
			t.Fatalf("updater 应为操作人 %d: %+v", adminID, item)
		}
	})

	t.Run("累加与负调整", func(t *testing.T) {
		status, item, _ := adjustBalance(t, env, admin, map[string]any{
			"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "-1", "reason": "扣回多给",
		})
		if status != http.StatusOK || item["adjustment"] != "1.50" {
			t.Fatalf("累加调整应 1.50: HTTP %d %+v", status, item)
		}
	})

	t.Run("原因必填", func(t *testing.T) {
		for _, reason := range []string{"", "   "} {
			status, _, msg := adjustBalance(t, env, admin, map[string]any{
				"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "1", "reason": reason,
			})
			if status != http.StatusBadRequest || msg == "" {
				t.Fatalf("原因 %q 应 400，实际 HTTP %d msg %q", reason, status, msg)
			}
		}
		// 完全缺失字段（binding:required）。
		status, _, _ := adjustBalance(t, env, admin, map[string]any{
			"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "1",
		})
		if status != http.StatusBadRequest {
			t.Fatalf("缺失 reason 应 400，实际 HTTP %d", status)
		}
	})

	t.Run("delta 校验", func(t *testing.T) {
		cases := []struct {
			name  string
			delta string
		}{
			{"零调整", "0"},
			{"两位小数", "0.25"},
			{"超范围", "10000"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, _, _ := adjustBalance(t, env, admin, map[string]any{
					"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": tc.delta, "reason": "测试",
				})
				if status != http.StatusBadRequest {
					t.Fatalf("delta %s 应 400，实际 HTTP %d", tc.delta, status)
				}
			})
		}
	})

	t.Run("对象校验", func(t *testing.T) {
		if status, _, msg := adjustBalance(t, env, admin, map[string]any{
			"userId": u1, "leaveTypeId": personalID, "year": 2026, "delta": "1", "reason": "测试",
		}); status != http.StatusBadRequest || msg == "" {
			t.Fatalf("不占额度类型调整应 400，实际 HTTP %d msg %q", status, msg)
		}
		if status, _, _ := adjustBalance(t, env, admin, map[string]any{
			"userId": u1, "leaveTypeId": 999999, "year": 2026, "delta": "1", "reason": "测试",
		}); status != http.StatusNotFound {
			t.Fatalf("不存在类型调整应 404，实际 HTTP %d", status)
		}
	})
}

// TestIntegrationLeaveBalanceMine 员工本人可见（登录即可）：只见自己的额度；
// 占用额度类型缺行时余额全零；不占额度类型 balance=null；年度缺省当前年。
func TestIntegrationLeaveBalanceMine(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, _ := setupLeaveTypes(t, env, admin)
	u1 := createUser(t, env, admin, "mine_u1", 101)
	createUser(t, env, admin, "mine_u2", 101) // 未初始化，验证缺行余额全零
	if status, created, _, _ := initBalances(t, env, admin, map[string]any{"year": 2026, "userIds": []int64{u1}}); status != http.StatusOK || created != 1 {
		t.Fatalf("初始化 u1：HTTP %d created %d", status, created)
	}
	// 给 u1 一笔调整，验证余额可见。
	if status, _, _ := adjustBalance(t, env, admin, map[string]any{
		"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "1.5", "reason": "奖励假",
	}); status != http.StatusOK {
		t.Fatalf("调整 u1：HTTP %d", status)
	}

	// 员工本人（无管理角色）登录查看。
	token := env.Login(t, "mine_u1", "123456")
	status, data, msg := myBalances(t, env, token, "?year=2026")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("我的额度：HTTP %d msg %q", status, msg)
	}
	if int(data["year"].(float64)) != 2026 {
		t.Fatalf("year 应回显 2026: %+v", data)
	}
	annual := mineItemOf(data, "annual")
	if annual == nil {
		t.Fatal("我的额度应包含 annual")
	}
	balance, _ := annual["balance"].(map[string]any)
	if balance == nil || balance["quota"] != "5.00" || balance["adjustment"] != "1.50" || balance["used"] != "0.00" || balance["available"] != "6.50" {
		t.Fatalf("annual 余额不符（初始5 + 调整1.5 − 已用0 = 6.5）: %+v", annual)
	}
	if balance["adjustReason"] != "奖励假" {
		t.Fatalf("余额应带最近调整原因: %+v", balance)
	}
	personal := mineItemOf(data, "personal")
	if personal == nil || personal["balance"] != nil || personal["balanceManaged"] != false {
		t.Fatalf("不占额度类型应 balance=null: %+v", personal)
	}

	// u2 尚未初始化：占用额度类型余额全零。
	token2 := env.Login(t, "mine_u2", "123456")
	status, data2, _ := myBalances(t, env, token2, "?year=2026")
	if status != http.StatusOK {
		t.Fatalf("u2 我的额度：HTTP %d", status)
	}
	annual2 := mineItemOf(data2, "annual")
	if annual2 == nil {
		t.Fatal("u2 应包含 annual 行")
	}
	balance2, _ := annual2["balance"].(map[string]any)
	if balance2 == nil || balance2["quota"] != "0.00" || balance2["available"] != "0.00" {
		t.Fatalf("未初始化类型余额应全零: %+v", annual2)
	}
	// u1 的调整不泄露给 u2。
	if balance2["adjustReason"] != "" {
		t.Fatalf("u2 不应看到 u1 的调整留痕: %+v", balance2)
	}

	// 非法年度 400。
	if status, _, _ := myBalances(t, env, token, "?year=1900"); status != http.StatusBadRequest {
		t.Fatalf("非法年度应 400，实际 HTTP %d", status)
	}
	// 年度缺省 = 当前年（2026，与测试数据无关，只验结构完整）。
	if status, _, _ = myBalances(t, env, token, ""); status != http.StatusOK {
		t.Fatalf("缺省年度应 200，实际 HTTP %d", status)
	}
}

// TestIntegrationLeaveBalancePageFilter 管理分页过滤：userId/leaveTypeId/year
// 组合过滤与 total 正确性。
func TestIntegrationLeaveBalancePageFilter(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, _ := setupLeaveTypes(t, env, admin)
	u1 := createUser(t, env, admin, "page_u1", 101)
	u2 := createUser(t, env, admin, "page_u2", 101)
	if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 2026, "userIds": []int64{u1, u2}}); status != http.StatusOK {
		t.Fatal("初始化失败")
	}
	if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 2025, "userIds": []int64{u1}}); status != http.StatusOK {
		t.Fatal("初始化 2025 失败")
	}

	if _, rows, total, _ := balancePage(t, env, admin, "?pageNo=1&pageSize=10"); total != 3 || len(rows) != 3 {
		t.Fatalf("全量分页 total=%d len=%d，期望 3/3", total, len(rows))
	}
	if _, _, total, _ := balancePage(t, env, admin, fmt.Sprintf("?userId=%d&year=2026", u1)); total != 1 {
		t.Fatalf("userId+year 过滤 total=%d，期望 1", total)
	}
	if _, _, total, _ := balancePage(t, env, admin, "?year=2025"); total != 1 {
		t.Fatalf("year=2025 过滤 total=%d，期望 1", total)
	}
	if _, _, total, _ := balancePage(t, env, admin, fmt.Sprintf("?leaveTypeId=%d", annualID)); total != 3 {
		t.Fatalf("leaveTypeId 过滤 total=%d，期望 3", total)
	}
	// 非管理用户 403（员工无 oa:leave:manage）。
	token := env.Login(t, "page_u1", "123456")
	if status, api := env.Request(t, http.MethodGet, "/oa/leave/balance/page", token, nil); status != http.StatusForbidden {
		t.Fatalf("无码用户分页应 403，实际 HTTP %d code %d", status, api.Code)
	}
}

// ---- 权限与租户隔离 ----

// TestIntegrationLeavePermissionCodes 权限码逐码（spec §4.6）：菜单 522 的
// oa:leave:manage 是类型与额度全部管理动作的唯一管理码——持码用户可管全
// 租户（任意用户的额度初始化/调整）；无码用户类型列表/本人余额登录即可，
// 管理动作全部 403。
func TestIntegrationLeavePermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	managerRole := createRoleWithMenus(t, env, admin, "leave_manager", []int64{leaveMenuID})
	noneRole := createRoleWithMenus(t, env, admin, "leave_none", []int64{1}) // 仪表盘
	manager := oaLoginNewUser(t, env, admin, "leave_mgr_u", managerRole)
	noPerm := oaLoginNewUser(t, env, admin, "leave_none_u", noneRole)
	employeeID := createUser(t, env, admin, "leave_emp2", 101)

	// 无码用户：登录即可用的两个查询放行；管理动作全 403。
	if status, _, msg := listLeaveTypes(t, env, noPerm, ""); status != http.StatusOK || msg != "" {
		t.Fatalf("无码用户类型列表应 200（登录即可），实际 HTTP %d msg %q", status, msg)
	}
	if status, _, msg := myBalances(t, env, noPerm, ""); status != http.StatusOK || msg != "" {
		t.Fatalf("无码用户本人余额应 200（登录即可），实际 HTTP %d msg %q", status, msg)
	}
	forbidden := func(phase string, status int, api testutil.APIResponse) {
		t.Helper()
		if status != http.StatusForbidden {
			t.Fatalf("%s 无码应 403，实际 HTTP %d msg %q", phase, status, api.Msg)
		}
	}
	status, api := env.Request(t, http.MethodPost, "/oa/leave/type", noPerm, map[string]any{"name": "X", "code": "x"})
	forbidden("创建类型", status, api)
	status, api = env.Request(t, http.MethodPut, "/oa/leave/type", noPerm, map[string]any{"id": 1, "name": "X"})
	forbidden("更新类型", status, api)
	status, api = env.Request(t, http.MethodPut, "/oa/leave/type/1/status", noPerm, map[string]any{"status": 1})
	forbidden("启停类型", status, api)
	status, api = env.Request(t, http.MethodDelete, "/oa/leave/type/1", noPerm, nil)
	forbidden("删除类型", status, api)
	status, api = env.Request(t, http.MethodGet, "/oa/leave/balance/page", noPerm, nil)
	forbidden("额度分页", status, api)
	status, api = env.Request(t, http.MethodPost, "/oa/leave/balance/init", noPerm, map[string]any{"year": 2026, "userIds": []int64{employeeID}})
	forbidden("额度初始化", status, api)
	status, api = env.Request(t, http.MethodPost, "/oa/leave/balance/adjust", noPerm, map[string]any{"userId": employeeID, "leaveTypeId": 1, "year": 2026, "delta": "1", "reason": "r"})
	forbidden("额度调整", status, api)

	// 持码用户（仅菜单 522）：类型与额度全链可管，且可管任意员工。
	status, annualID, msg := createLeaveType(t, env, manager, map[string]any{
		"name": "调休", "code": "comp-time", "balanceManaged": true, "annualQuota": "0",
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("manager 创建类型：HTTP %d msg %q", status, msg)
	}
	if _, rows, _ := listLeaveTypes(t, env, manager, ""); len(rows) == 0 {
		t.Fatal("manager 类型列表应非空")
	}
	var created int
	status, created, _, _ = initBalances(t, env, manager, map[string]any{"year": 2026, "userIds": []int64{employeeID}})
	if status != http.StatusOK || created != 1 {
		t.Fatalf("manager 初始化他人额度：HTTP %d created %d", status, created)
	}
	if status, item, msg := adjustBalance(t, env, manager, map[string]any{
		"userId": employeeID, "leaveTypeId": annualID, "year": 2026, "delta": "0.5", "reason": "manager 调整",
	}); status != http.StatusOK || item["available"] != "0.50" || msg != "" {
		t.Fatalf("manager 调整他人额度：HTTP %d %+v msg %q", status, item, msg)
	}
	if _, rows, total, _ := balancePage(t, env, manager, fmt.Sprintf("?userId=%d", employeeID)); total != 1 || len(rows) != 1 {
		t.Fatalf("manager 分页：total %d len %d，期望 1/1", total, len(rows))
	}
	if status, _ := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/leave/type/%d/status", annualID), manager, map[string]any{"status": 1}); status != http.StatusOK {
		t.Fatalf("manager 启停：HTTP %d", status)
	}
}

// TestIntegrationLeaveTenantIsolation 租户隔离（spec §4.6）：跨租户业务 ID
// 一律 404 不泄露存在性；同码/同年度额度互不冲突互不可见。
func TestIntegrationLeaveTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	annualID, personalID := setupLeaveTypes(t, env, admin)
	u1 := createUser(t, env, admin, "iso_u1", 101)
	if status, _, _, _ := initBalances(t, env, admin, map[string]any{"year": 2026, "userIds": []int64{u1}}); status != http.StatusOK {
		t.Fatal("初始化失败")
	}
	if status, _, _ := adjustBalance(t, env, admin, map[string]any{
		"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "2", "reason": "租户1调整",
	}); status != http.StatusOK {
		t.Fatal("调整失败")
	}

	tenant2 := createOATenant(t, env, admin, "leave_isolate_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)

	// 类型列表互不可见。
	_, rows2, _ := listLeaveTypes(t, env, token2, "")
	if leaveTypeOf(rows2, "annual") != nil || leaveTypeOf(rows2, "personal") != nil {
		t.Fatalf("租户 2 不应看到租户 1 的类型: %v", rows2)
	}
	// 租户 2 建同码类型不冲突（唯一键按租户隔离）。
	status, t2AnnualID, msg := createLeaveType(t, env, token2, map[string]any{
		"name": "年假", "code": "annual", "balanceManaged": true, "annualQuota": "10",
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("租户 2 同码创建应成功：HTTP %d msg %q", status, msg)
	}
	// 跨租户类型操作一律 404（不泄露存在性）。
	if status, _ := env.Request(t, http.MethodPut, "/oa/leave/type", token2, map[string]any{
		"id": annualID, "name": "越权改", "balanceManaged": true,
	}); status != http.StatusNotFound {
		t.Fatalf("跨租户更新类型应 404，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/leave/type/%d/status", annualID), token2, map[string]any{"status": 1}); status != http.StatusNotFound {
		t.Fatalf("跨租户启停类型应 404，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/leave/type/%d", annualID), token2, nil); status != http.StatusNotFound {
		t.Fatalf("跨租户删除类型应 404，实际 HTTP %d", status)
	}
	// 跨租户额度操作 404（类型 404 先行）。
	if status, _, _ := adjustBalance(t, env, token2, map[string]any{
		"userId": u1, "leaveTypeId": annualID, "year": 2026, "delta": "1", "reason": "跨租户",
	}); status != http.StatusNotFound {
		t.Fatalf("跨租户调整额度应 404，实际 HTTP %d", status)
	}
	// 额度分页互不可见：租户 2 全量为空。
	if _, rows, total, _ := balancePage(t, env, token2, "?pageNo=1&pageSize=10"); total != 0 || len(rows) != 0 {
		t.Fatalf("租户 2 额度分页应为空，实际 total=%d", total)
	}
	// 租户 2 自己的员工余额独立：初始化 → 调整 → 本人视图可用 11.5，
	// 租户 1 的额度与调整不可见。
	emp2 := createUserInTenant(t, env, token2, "iso_t2_emp")
	if status, created, _, _ := initBalances(t, env, token2, map[string]any{"year": 2026, "userIds": []int64{emp2}}); status != http.StatusOK || created != 1 {
		t.Fatalf("租户 2 初始化：HTTP %d created %d", status, created)
	}
	if status, _, _ := adjustBalance(t, env, token2, map[string]any{
		"userId": emp2, "leaveTypeId": t2AnnualID, "year": 2026, "delta": "1.5", "reason": "租户2调整",
	}); status != http.StatusOK {
		t.Fatalf("租户 2 调整：HTTP %d", status)
	}
	if status, data, _ := myBalances(t, env, loginOATenant(t, env, "iso_t2_emp", "123456", tenant2), "?year=2026"); status != http.StatusOK {
		t.Fatalf("租户 2 员工我的额度：HTTP %d", status)
	} else if annual := mineItemOf(data, "annual"); annual == nil {
		t.Fatal("租户 2 员工应看到 annual")
	} else if balance, _ := annual["balance"].(map[string]any); balance == nil || balance["available"] != "11.50" {
		t.Fatalf("租户 2 annual 可用应 11.50（与租户 1 互不可见）: %+v", annual)
	}
	_ = personalID
}

// createUserInTenant 用指定租户管理员创建启用用户并回读 ID。
func createUserInTenant(t *testing.T, env *testutil.IntegrationEnv, tenantAdmin, username string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", tenantAdmin, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": 0, "roleIds": []int64{}, "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户内创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet,
		"/system/user/page?username="+username+"&pageNo=1&pageSize=10", tenantAdmin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询用户 %s：HTTP %d code %d", username, status, api.Code)
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("用户 %s 应唯一命中: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID
}
