//go:build integration

// 报销草稿与明细的 HTTP 集成用例（issue #54，docs/oa-mvp-spec.md §4.9）：
//   - 草稿创建/编辑/删除；明细行增删改与排序（整体替换语义）；
//   - 表头金额恒等于明细合计，请求里的手填 totalAmount 被忽略；
//   - 无明细、零/负金额、字典外类别、非法日期一律 400；
//   - 删除级联软删明细；权限码逐码（create/update/delete）；
//   - 租户隔离与可见范围（本人 / 部门负责人子树 / manage）；
//   - 附件上传/下载/删除（infra_file，biz_type=oa_expense；非草稿冻结）。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t54，-p 1 串行跑包）。
package oa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 报销菜单/按钮码（菜单 523 + 5231–5235，员工页 535；issue #54 种子）。
// 535 是合并 #62 后的空位（533 考勤结果、534 考勤汇总已被占用）。
const (
	expenseMenuID     = int64(523)
	expenseCreate     = int64(5231)
	expenseUpdate     = int64(5232)
	expenseDelete     = int64(5233)
	expenseSubmit     = int64(5234)
	expenseManage     = int64(5235)
	expenseMineMenuID = int64(535)
)

// expenseClaimResp 是报销单详情/保存响应的测试视图。
type expenseClaimResp struct {
	ID                 int64  `json:"id"`
	UserID             int64  `json:"userId"`
	Title              string `json:"title"`
	TotalAmount        string `json:"totalAmount"`
	Reason             string `json:"reason"`
	Status             int8   `json:"status"`
	RejectReason       string `json:"rejectReason"`
	ApprovalInstanceID *int64 `json:"approvalInstanceId"`
	ItemCount          int    `json:"itemCount"`
	Items              []struct {
		ID          int64  `json:"id"`
		Category    string `json:"category"`
		Amount      string `json:"amount"`
		ExpenseDate string `json:"expenseDate"`
		Remark      string `json:"remark"`
		Sort        int    `json:"sort"`
	} `json:"items"`
	Files []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"files"`
}

// expenseItemBody 构造一条明细请求。amount 必须是十进制字符串（money.Money
// 拒绝 JSON 数字，避免 JavaScript double 丢精度）。
func expenseItemBody(category, amount, date, remark string) map[string]any {
	return map[string]any{
		"category":    category,
		"amount":      amount,
		"expenseDate": date,
		"remark":      remark,
	}
}

// expenseClaimBody 构造报销单保存请求：totalAmount 故意填错（服务端应忽略）。
func expenseClaimBody(title, reason string, items ...map[string]any) map[string]any {
	return map[string]any{
		"title":       title,
		"reason":      reason,
		"totalAmount": "9999.00",
		"items":       items,
	}
}

// createExpenseClaimAPI 创建草稿并断言成功，返回单据 ID。
func createExpenseClaimAPI(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/expense/claim", token, body)
	mustOK(t, "创建报销草稿", status, api)
	return decodeExpenseClaim(t, api).ID
}

// decodeExpenseClaim 解析报销单响应。
func decodeExpenseClaim(t *testing.T, api testutil.APIResponse) expenseClaimResp {
	t.Helper()
	var out expenseClaimResp
	if err := json.Unmarshal(api.Data, &out); err != nil {
		t.Fatalf("解析报销单响应: %v（data=%s）", err, string(api.Data))
	}
	return out
}

// getExpenseClaimAPI 读报销单详情。
func getExpenseClaimAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, expenseClaimResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/expense/claim/%d", id), token, nil)
	if api.Code != 0 {
		return status, expenseClaimResp{}
	}
	return status, decodeExpenseClaim(t, api)
}

// updateExpenseClaimAPI 更新草稿并断言成功。
func updateExpenseClaimAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, body map[string]any) expenseClaimResp {
	t.Helper()
	body["id"] = id
	status, api := env.Request(t, http.MethodPut, "/oa/expense/claim", token, body)
	mustOK(t, "更新报销草稿", status, api)
	return decodeExpenseClaim(t, api)
}

// pageExpenseClaimsAPI 分页并返回 total。
func pageExpenseClaimsAPI(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, int64, []expenseClaimResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/expense/claim/page?pageNo=1&pageSize=20"+query, token, nil)
	if api.Code != 0 {
		return status, 0, nil
	}
	var page struct {
		List  []expenseClaimResp `json:"list"`
		Total int64              `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析报销分页: %v（data=%s）", err, string(api.Data))
	}
	return status, page.Total, page.List
}

// uploadExpenseFile 以 multipart/form-data 上传报销附件。
func uploadExpenseFile(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, filename string, content []byte) (int, testutil.APIResponse) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL(fmt.Sprintf("/oa/expense/claim/%d/files", id)), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传报销附件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	return resp.StatusCode, api
}

// downloadExpenseFile 按响应里的鉴权 URL 下载附件。
func downloadExpenseFile(t *testing.T, env *testutil.IntegrationEnv, token, fileURL string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.Server.URL+fileURL, nil)
	if err != nil {
		t.Fatalf("构造下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载报销附件: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取下载内容: %v", err)
	}
	return resp.StatusCode, body
}

// expenseUser 建一个带报销基础按钮码的用户（默认部门 101）。
func expenseUser(t *testing.T, env *testutil.IntegrationEnv, admin, username string, menuIDs []int64) (int64, string) {
	t.Helper()
	return createUserWithMenus(t, env, admin, username, 101, menuIDs)
}

// ---------- 主流程 ----------

// TestIntegrationExpenseDraftCRUDAndTotal 草稿闭环：
// 创建（手填合计被忽略）→ 增行 → 改行/排序 → 删行 → 合计随明细重算 →
// 删除级联软删明细。
func TestIntegrationExpenseDraftCRUDAndTotal(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, token := expenseUser(t, env, admin, "expense_crud_emp", []int64{
		expenseMineMenuID, expenseCreate, expenseUpdate, expenseDelete,
	})

	// 创建：两行 12.34 + 0.66 = 13.00，请求里的 totalAmount=9999.00 被忽略。
	status, api := env.Request(t, http.MethodPost, "/oa/expense/claim", token,
		expenseClaimBody("十月差旅", "客户拜访",
			expenseItemBody("交通", "12.34", "2026-10-08", "高铁"),
			expenseItemBody("餐饮", "0.66", "2026-10-09", ""),
		))
	mustOK(t, "创建报销草稿", status, api)
	created := decodeExpenseClaim(t, api)
	if created.ID <= 0 {
		t.Fatalf("创建报销单应返回 ID，实际 %+v", created)
	}
	if created.TotalAmount != "13.00" {
		t.Fatalf("表头金额 = %s，期望按明细合计 13.00（手填金额应被忽略）", created.TotalAmount)
	}
	if created.ItemCount != 2 || len(created.Items) != 2 {
		t.Fatalf("明细数 = itemCount %d / items %d，期望 2", created.ItemCount, len(created.Items))
	}
	if created.Status != 0 {
		t.Fatalf("新建草稿状态 = %d，期望 0", created.Status)
	}
	if created.Items[0].Category != "交通" || created.Items[0].Sort != 0 ||
		created.Items[1].Category != "餐饮" || created.Items[1].Sort != 1 {
		t.Fatalf("明细顺序/排序不符：%+v", created.Items)
	}

	// 列表：1 条，合计与明细一致。
	status, total, list := pageExpenseClaimsAPI(t, env, token, "")
	if status != http.StatusOK || total != 1 || len(list) != 1 {
		t.Fatalf("报销列表：HTTP %d total %d len %d，期望 1 条", status, total, len(list))
	}
	if list[0].TotalAmount != "13.00" || list[0].ItemCount != 2 {
		t.Fatalf("列表行合计/明细数不符：%+v", list[0])
	}

	// 增行 + 改行 + 重排：1.00 + 100.00 + 0.50 = 101.50，顺序按请求。
	updated := updateExpenseClaimAPI(t, env, token, created.ID,
		expenseClaimBody("十月差旅（补充）", "客户拜访与住宿",
			expenseItemBody("交通", "1.00", "2026-10-08", "地铁"),
			expenseItemBody("住宿", "100.00", "2026-10-08", "两晚"),
			expenseItemBody("餐饮", "0.50", "2026-10-09", ""),
		))
	if updated.TotalAmount != "101.50" {
		t.Fatalf("增行后合计 = %s，期望 101.50", updated.TotalAmount)
	}
	if updated.ItemCount != 3 || len(updated.Items) != 3 {
		t.Fatalf("增行后明细数 = %d，期望 3", updated.ItemCount)
	}
	if updated.Items[1].Category != "住宿" || updated.Items[1].Sort != 1 {
		t.Fatalf("重排后第 2 行应为住宿 sort=1，实际 %+v", updated.Items[1])
	}

	// 删行：只留一行 50.00，合计重算。
	trimmed := updateExpenseClaimAPI(t, env, token, created.ID,
		expenseClaimBody("十月差旅（精简）", "仅交通",
			expenseItemBody("交通", "50.00", "2026-10-08", ""),
		))
	if trimmed.TotalAmount != "50.00" || trimmed.ItemCount != 1 {
		t.Fatalf("删行后合计/明细数 = %s/%d，期望 50.00/1", trimmed.TotalAmount, trimmed.ItemCount)
	}
	status, detail := getExpenseClaimAPI(t, env, token, created.ID)
	if status != http.StatusOK || len(detail.Items) != 1 || detail.Items[0].Amount != "50.00" {
		t.Fatalf("详情：HTTP %d items %+v", status, detail.Items)
	}
	if detail.Files == nil {
		t.Fatal("详情 files 应为空数组而非 null")
	}

	// 删除：级联软删明细，详情 404。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/expense/claim/%d", created.ID), token, nil)
	mustOK(t, "删除报销草稿", status, api)
	if status, _ := getExpenseClaimAPI(t, env, token, created.ID); status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 %d", status)
	}
	if _, total, _ := pageExpenseClaimsAPI(t, env, token, ""); total != 0 {
		t.Fatalf("删除后列表应为空，实际 total=%d", total)
	}
	var active, all int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM `oa_expense_item` WHERE `claim_id` = ? AND `deleted_at` IS NULL", created.ID).Scan(&active).Error; err != nil {
		t.Fatalf("统计有效明细: %v", err)
	}
	if err := env.DB.Raw("SELECT COUNT(*) FROM `oa_expense_item` WHERE `claim_id` = ?", created.ID).Scan(&all).Error; err != nil {
		t.Fatalf("统计明细总行: %v", err)
	}
	if active != 0 {
		t.Fatalf("删除后仍有效明细 %d 行，期望 0（级联软删）", active)
	}
	// 创建 2 + 增行 3 + 删行 1 = 6 行历史（软删保留）。
	if all != 6 {
		t.Fatalf("明细历史行数 = %d，期望 6（整单替换保留软删痕迹）", all)
	}
}

// TestIntegrationExpenseDraftRejections 拒绝矩阵（spec §4.9）：
// 无明细、零/负金额、字典外类别、非法日期、超长字段、数字型金额一律 400，
// 且不留下任何单据行。
func TestIntegrationExpenseDraftRejections(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, token := expenseUser(t, env, admin, "expense_reject_emp", []int64{
		expenseMineMenuID, expenseCreate,
	})

	cases := []struct {
		name      string
		body      map[string]any
		wantInMsg string
	}{
		{
			name:      "无明细被拒绝",
			body:      expenseClaimBody("空单", "无明细"),
			wantInMsg: "明细",
		},
		{
			name: "零金额被拒绝",
			body: expenseClaimBody("零金额", "测试",
				expenseItemBody("交通", "0.00", "2026-10-08", "")),
			wantInMsg: "大于 0",
		},
		{
			name: "负金额被拒绝",
			body: expenseClaimBody("负金额", "测试",
				expenseItemBody("交通", "-1.00", "2026-10-08", "")),
			wantInMsg: "大于 0",
		},
		{
			name: "字典外类别被拒绝",
			body: expenseClaimBody("类别", "测试",
				expenseItemBody("机票", "10.00", "2026-10-08", "")),
			wantInMsg: "oa_expense_category",
		},
		{
			name: "非法日期被拒绝",
			body: expenseClaimBody("日期", "测试",
				expenseItemBody("交通", "10.00", "2026/10/08", "")),
			wantInMsg: "YYYY-MM-DD",
		},
		{
			name: "空标题被拒绝",
			body: expenseClaimBody("", "测试",
				expenseItemBody("交通", "10.00", "2026-10-08", "")),
			wantInMsg: "",
		},
		{
			name: "空事由被拒绝",
			body: expenseClaimBody("标题", "",
				expenseItemBody("交通", "10.00", "2026-10-08", "")),
			wantInMsg: "",
		},
		{
			name: "超长明细备注被拒绝",
			body: expenseClaimBody("备注", "测试",
				expenseItemBody("交通", "10.00", "2026-10-08", strings.Repeat("备", 201))),
			wantInMsg: "备注",
		},
		{
			name: "金额为 JSON 数字被拒绝",
			body: map[string]any{
				"title": "数字金额", "reason": "测试",
				"items": []map[string]any{{
					"category": "交通", "amount": 12.34, "expenseDate": "2026-10-08",
				}},
			},
			wantInMsg: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, api := env.Request(t, http.MethodPost, "/oa/expense/claim", token, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
			}
			if tc.wantInMsg != "" && !strings.Contains(api.Msg, tc.wantInMsg) {
				t.Fatalf("错误信息应包含 %q，实际 %q", tc.wantInMsg, api.Msg)
			}
		})
	}
	if _, total, _ := pageExpenseClaimsAPI(t, env, token, ""); total != 0 {
		t.Fatalf("拒绝路径不应落库，实际 total=%d", total)
	}
}

// TestIntegrationExpensePermissionCodes 权限码逐码控权：
// 查询码（菜单 523）可读列表；创建/编辑/删除逐码；无码 403。
func TestIntegrationExpensePermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	ownerID, owner := expenseUser(t, env, admin, "expense_code_owner", []int64{
		expenseMenuID, expenseCreate, expenseUpdate, expenseDelete,
	})
	claimID := createExpenseClaimAPI(t, env, owner,
		expenseClaimBody("权限码报销", "测试",
			expenseItemBody("交通", "10.00", "2026-10-08", "")))
	if ownerID <= 0 || claimID <= 0 {
		t.Fatal("前置数据创建失败")
	}

	readerRoleID := createRoleWithMenus(t, env, admin, "expense_reader", []int64{expenseMenuID})
	reader := oaLoginNewUser(t, env, admin, "expense_code_reader", readerRoleID)

	// 读者（仅查询码）：列表可读，写动作全部 403。
	if status, _, _ := pageExpenseClaimsAPI(t, env, reader, ""); status != http.StatusOK {
		t.Fatalf("查询码用户读列表应 200，实际 %d", status)
	}
	if status, api := env.Request(t, http.MethodPost, "/oa/expense/claim", reader,
		expenseClaimBody("越权创建", "测试",
			expenseItemBody("交通", "10.00", "2026-10-08", ""))); status != http.StatusForbidden {
		t.Fatalf("无 create 码创建应 403，实际 %d code %d msg %q", status, api.Code, api.Msg)
	}
	body := expenseClaimBody("越权编辑", "测试", expenseItemBody("交通", "10.00", "2026-10-08", ""))
	body["id"] = claimID
	if status, _ := env.Request(t, http.MethodPut, "/oa/expense/claim", reader, body); status != http.StatusForbidden {
		t.Fatalf("无 update 码编辑应 403，实际 %d", status)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/expense/claim/%d", claimID), reader, nil); status != http.StatusForbidden {
		t.Fatalf("无 delete 码删除应 403，实际 %d", status)
	}

	// 编辑码：可编辑自己的单据、不可删除（无 delete 码）。
	editorRole := createRoleWithMenus(t, env, admin, "expense_editor",
		[]int64{expenseMenuID, expenseCreate, expenseUpdate})
	editor := oaLoginNewUser(t, env, admin, "expense_code_editor", editorRole)
	editorClaimID := createExpenseClaimAPI(t, env, editor,
		expenseClaimBody("编辑码单据", "测试", expenseItemBody("交通", "10.00", "2026-10-08", "")))
	body = expenseClaimBody("编辑码更新", "测试", expenseItemBody("交通", "20.00", "2026-10-08", ""))
	body["id"] = editorClaimID
	if status, api := env.Request(t, http.MethodPut, "/oa/expense/claim", editor, body); status != http.StatusOK {
		t.Fatalf("编辑码用户更新应 200，实际 %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/expense/claim/%d", editorClaimID), editor, nil); status != http.StatusForbidden {
		t.Fatalf("无 delete 码删除应 403，实际 %d", status)
	}
	// 他人的单据即使有 update 码也不可编辑（写动作按本人收口，404 不泄露存在性）。
	body = expenseClaimBody("越权编辑他人", "测试", expenseItemBody("交通", "20.00", "2026-10-08", ""))
	body["id"] = claimID
	if status, _ := env.Request(t, http.MethodPut, "/oa/expense/claim", editor, body); status != http.StatusNotFound {
		t.Fatalf("编辑他人单据应 404，实际 %d", status)
	}
}

// TestIntegrationExpenseTenantIsolation 跨租户：列表不可见；详情/编辑/删除/
// 附件一律 404，不泄露存在性。
func TestIntegrationExpenseTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, owner := expenseUser(t, env, admin, "expense_tenant_owner", []int64{
		expenseMenuID, expenseCreate, expenseUpdate, expenseDelete,
	})
	claimID := createExpenseClaimAPI(t, env, owner,
		expenseClaimBody("租户一报销", "测试",
			expenseItemBody("交通", "88.00", "2026-10-08", "")))

	tenant2 := createOATenant(t, env, admin, "expense_isolate_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)

	if _, total, _ := pageExpenseClaimsAPI(t, env, token2, ""); total != 0 {
		t.Fatalf("跨租户报销列表应为空，实际 total=%d", total)
	}
	if status, _ := getExpenseClaimAPI(t, env, token2, claimID); status != http.StatusNotFound {
		t.Fatalf("跨租户详情应 404，实际 %d", status)
	}
	body := expenseClaimBody("越权", "测试", expenseItemBody("交通", "1.00", "2026-10-08", ""))
	body["id"] = claimID
	if status, api := env.Request(t, http.MethodPut, "/oa/expense/claim", token2, body); status != http.StatusNotFound {
		t.Fatalf("跨租户编辑应 404，实际 %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/expense/claim/%d", claimID), token2, nil); status != http.StatusNotFound {
		t.Fatalf("跨租户删除应 404，实际 %d", status)
	}
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/expense/claim/%d/files", claimID), token2, nil); status != http.StatusNotFound {
		t.Fatalf("跨租户附件列表应 404，实际 %d", status)
	}
	// 原租户单据未被跨租户删除影响。
	if status, _ := getExpenseClaimAPI(t, env, owner, claimID); status != http.StatusOK {
		t.Fatalf("租户一单据应仍可见，实际 %d", status)
	}
}

// TestIntegrationExpenseVisibilityScope 可见范围（spec §4.6）：
// 部门负责人看到子树内下属的报销单；非负责人（无 manage）只看本人；
// 范围外详情 404。
func TestIntegrationExpenseVisibilityScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	deptID := createSubDept(t, env, admin, "报销一部", 101)

	employeeID, employee := createUserWithMenus(t, env, admin, "expense_scope_emp", deptID,
		[]int64{expenseMineMenuID, expenseCreate})
	leaderID, leader := createUserWithMenus(t, env, admin, "expense_scope_leader", deptID,
		[]int64{expenseMineMenuID})
	_, outsider := createUserWithMenus(t, env, admin, "expense_scope_out", 101,
		[]int64{expenseMineMenuID})
	setDeptLeader(t, env, admin, deptID, leaderID)

	claimID := createExpenseClaimAPI(t, env, employee,
		expenseClaimBody("下属报销", "出差",
			expenseItemBody("住宿", "300.00", "2026-10-08", "")))

	// 负责人：列表可见（子树 ∪ 本人），详情可见。
	_, total, list := pageExpenseClaimsAPI(t, env, leader, "")
	if total != 1 || len(list) != 1 || list[0].UserID != employeeID {
		t.Fatalf("部门负责人列表应看到下属 1 条，实际 total=%d list=%+v", total, list)
	}
	if status, detail := getExpenseClaimAPI(t, env, leader, claimID); status != http.StatusOK || detail.UserID != employeeID {
		t.Fatalf("部门负责人读下属详情：HTTP %d user %d", status, detail.UserID)
	}

	// 非负责人 / 范围外：列表为空，详情 404。
	if _, total, _ := pageExpenseClaimsAPI(t, env, outsider, ""); total != 0 {
		t.Fatalf("非负责人列表应为空，实际 total=%d", total)
	}
	if status, _ := getExpenseClaimAPI(t, env, outsider, claimID); status != http.StatusNotFound {
		t.Fatalf("范围外详情应 404，实际 %d", status)
	}

	// 本人：可见。
	if status, _ := getExpenseClaimAPI(t, env, employee, claimID); status != http.StatusOK {
		t.Fatalf("本人详情应 200，实际 %d", status)
	}
}

// TestIntegrationExpenseFiles 附件：上传/列表/下载/删除；非本人不可见；
// 非草稿（提交后）冻结上传与删除。
func TestIntegrationExpenseFiles(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, owner := expenseUser(t, env, admin, "expense_file_owner", []int64{
		expenseMineMenuID, expenseCreate, expenseUpdate, expenseDelete,
	})
	_, other := expenseUser(t, env, admin, "expense_file_other", []int64{
		expenseMineMenuID, expenseCreate, expenseUpdate,
	})
	claimID := createExpenseClaimAPI(t, env, owner,
		expenseClaimBody("附件报销", "测试",
			expenseItemBody("办公", "66.00", "2026-10-08", "")))

	status, api := uploadExpenseFile(t, env, owner, claimID, "invoice.txt", []byte("invoice-content"))
	mustOK(t, "上传报销附件", status, api)
	var uploaded struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(api.Data, &uploaded); err != nil || uploaded.ID <= 0 {
		t.Fatalf("解析上传响应: %v（data=%s）", err, string(api.Data))
	}

	status, detail := getExpenseClaimAPI(t, env, owner, claimID)
	if status != http.StatusOK || len(detail.Files) != 1 || detail.Files[0].ID != uploaded.ID {
		t.Fatalf("详情附件不符：HTTP %d files=%+v", status, detail.Files)
	}
	status, content := downloadExpenseFile(t, env, owner, detail.Files[0].URL)
	if status != http.StatusOK || string(content) != "invoice-content" {
		t.Fatalf("下载附件：HTTP %d content %q", status, string(content))
	}

	// 同租户非本人：列表/下载/删除 404（本工单可见范围不含该用户）。
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/expense/claim/%d/files", claimID), other, nil); status != http.StatusNotFound {
		t.Fatalf("非本人附件列表应 404，实际 %d", status)
	}
	if status, _ := downloadExpenseFile(t, env, other, detail.Files[0].URL); status != http.StatusNotFound {
		t.Fatalf("非本人下载应 404，实际 %d", status)
	}
	if status, _ := env.Request(t, http.MethodDelete,
		fmt.Sprintf("/oa/expense/claim/%d/files/%d", claimID, uploaded.ID), other, nil); status != http.StatusNotFound {
		t.Fatalf("非本人删除附件应 404，实际 %d", status)
	}

	// 提交后（状态待审）：上传/删除 409（明细与材料随审批冻结）。
	if err := env.DB.Exec("UPDATE `oa_expense_claim` SET `status` = ? WHERE `id` = ?",
		int8(1), claimID).Error; err != nil {
		t.Fatalf("置待审状态: %v", err)
	}
	if status, _ := uploadExpenseFile(t, env, owner, claimID, "late.txt", []byte("late")); status != http.StatusConflict {
		t.Fatalf("待审状态下上传应 409，实际 %d", status)
	}
	if status, _ := env.Request(t, http.MethodDelete,
		fmt.Sprintf("/oa/expense/claim/%d/files/%d", claimID, uploaded.ID), owner, nil); status != http.StatusConflict {
		t.Fatalf("待审状态下删除附件应 409，实际 %d", status)
	}
	// 待审也不可编辑/删除单据（状态守卫）。
	body := expenseClaimBody("待审编辑", "测试", expenseItemBody("办公", "1.00", "2026-10-08", ""))
	body["id"] = claimID
	if status, _ := env.Request(t, http.MethodPut, "/oa/expense/claim", owner, body); status != http.StatusConflict {
		t.Fatalf("待审状态下编辑应 409，实际 %d", status)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/expense/claim/%d", claimID), owner, nil); status != http.StatusConflict {
		t.Fatalf("待审状态下删除应 409，实际 %d", status)
	}

	// 回到草稿后删除附件成功。
	if err := env.DB.Exec("UPDATE `oa_expense_claim` SET `status` = ? WHERE `id` = ?",
		int8(0), claimID).Error; err != nil {
		t.Fatalf("恢复草稿状态: %v", err)
	}
	if status, api := env.Request(t, http.MethodDelete,
		fmt.Sprintf("/oa/expense/claim/%d/files/%d", claimID, uploaded.ID), owner, nil); status != http.StatusOK {
		t.Fatalf("草稿删除附件：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if _, detail := getExpenseClaimAPI(t, env, owner, claimID); len(detail.Files) != 0 {
		t.Fatalf("删除后附件应为空，实际 %+v", detail.Files)
	}
}
