//go:build integration

// CRM 端到端 HTTP 验收（issue #21）：真实 router + 生产中间件（JWT/权限/租户
// 插件）+ 真实 MySQL，以一个「销售」账号跑通验收标准里的全流程：
//
//	登录 → 建客户 → 加联系人 → 写跟进 → 建产品/分类 → 建商机并推进阶段
//	→ 建合同（行项目）→ 提交/审批 → 建回款计划 → 登记回款记录
//	→ 查仪表盘（卡片/漏斗/趋势非零）→ 公海释放/认领 → 触发回收任务
//	（有未作废合同不回收 / 无合同且超期回收）。
//
// 该用例同时是 #21 的自动化验收证据；演示数据本身由 internal/seed 的集成
// 用例覆盖，这里只依赖基础种子（租户/菜单/角色/admin）。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// e2eCRMMenuIDs 是销售角色需要的 CRM 目录/菜单/按钮：
// 400 目录 + 客户/联系人/跟进/公海/公海配置/产品/分类/商机/合同/回款/
// 仪表盘与导入导出按钮，与 seedMenus 的 CRM 段一致。
var e2eCRMMenuIDs = []int64{
	400,
	401, 4011, 4012, 4013, 4014, 4015, 4016, 4017,
	4030, 4031,
	4040, 4041, 4042, 4043,
	402, 4021, 4050,
	4060, 4061, 4063, 4064, 4065, 4066,
	4062, 4067, 4068, 4069, 4070,
	4080, 4081, 4082, 4083, 4084, 4085,
	4100, 4101, 4102, 4103, 4104, 4105,
	4120, 4121, 4122, 4123, 4124,
	4140, 4141,
	4160, 4161, 4162, 4163, 4164, 4165, 4166,
}

// TestIntegrationCRMFullFlow 是 #21 的端到端验收用例。
func TestIntegrationCRMFullFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 一个仅本人数据范围的销售账号：全程用它完成业务动作。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "端到端验收销售", "code": "e2e_sales", "dataScope": 5,
		"menuIds": e2eCRMMenuIDs,
	})
	createUserByAPI(t, env, admin, "e2e_sales", 102, []int64{roleID})
	sales := env.Login(t, "e2e_sales", "123456")

	// 1. 建客户。
	customer := createCustomer(t, env, sales, "端到端验收客户", map[string]any{
		"industry": "软件与信息服务", "level": "A", "source": "展会",
		"phone": "13800002001", "remark": "issue #21 全流程验收",
	})
	customerID := int64(customer["id"].(float64))
	ownerID := int64(customer["ownerUserId"].(float64))
	if customer["customerNo"] == "" || ownerID == 0 {
		t.Fatalf("客户应自动发放编号并归属当前销售：%v", customer)
	}

	// 2. 加联系人。
	contact := createContact(t, env, sales, customerID, "端到端联系人", map[string]any{
		"position": "信息总监", "mobile": "13900002001", "email": "e2e@example.com", "gender": 1,
	})
	if int64(contact["customerId"].(float64)) != customerID {
		t.Fatalf("联系人应挂在客户 %d 下：%v", customerID, contact)
	}

	// 3. 写跟进（客户维度），并确认 last_follow_time 被刷新。
	createFollowUp(t, env, sales, customerID, "首次电话沟通，确认采购意向。", map[string]any{
		"type": 1, "nextFollowTime": time.Now().AddDate(0, 0, 3).Format("2006-01-02"),
	})
	detail := e2eCustomerDetail(t, env, sales, customerID)
	if detail["lastFollowTime"] == nil || detail["lastFollowTime"] == "" {
		t.Fatal("新增跟进应刷新客户 last_follow_time")
	}

	// 4. 建产品分类与产品（合同/商机行项目引用）。
	category := e2eCreateProductCategory(t, env, sales, "端到端验收产品分类")
	product := e2eCreateProduct(t, env, sales, "端到端验收产品", map[string]any{
		"categoryId": category["id"], "code": "E2E-SKU-001",
		"unit": "套", "price": "128000.00", "status": 1,
	})
	productID := int64(product["id"].(float64))

	// 5. 建商机并推进阶段 1 → 5（赢单）；终态后禁止再推进。
	opportunity := createOpportunity(t, env, sales, "端到端验收商机", map[string]any{
		"customerId": customerID, "expectedAmount": "128000.00",
		"expectedCloseDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
		"products": []map[string]any{
			{"productId": productID, "quantity": 1, "price": "128000.00"},
		},
	})
	opportunityID := int64(opportunity["id"].(float64))
	for stage := int8(2); stage <= 5; stage++ {
		status, api := advanceOpportunityStage(t, env, sales, opportunityID, stage)
		mustOK(t, fmt.Sprintf("商机推进到阶段 %d", stage), status, api)
	}
	if status, _ := advanceOpportunityStage(t, env, sales, opportunityID, 6); status != http.StatusBadRequest {
		t.Fatalf("赢单后再推进阶段应 400，实际 HTTP %d", status)
	}
	// 另一个进行中的商机，保证仪表盘「进行中商机金额」非零。
	inProgress := createOpportunity(t, env, sales, "端到端进行中商机", map[string]any{
		"customerId": customerID, "stage": 2, "expectedAmount": "68000.00",
	})
	if inProgress["stage"] != float64(2) {
		t.Fatalf("进行中商机阶段应为 2：%v", inProgress)
	}

	// 6. 建合同（行项目）→ 提交审批 → 审批通过。
	signDate := time.Now().Format("2006-01-02")
	contract := createContract(t, env, sales, "端到端验收合同", map[string]any{
		"customerId": customerID, "opportunityId": opportunityID,
		"signDate": signDate, "startDate": signDate,
		"endDate": time.Now().AddDate(1, 0, 0).Format("2006-01-02"),
		"products": []map[string]any{
			{"productId": productID, "quantity": 1, "price": "128000.00"},
		},
	})
	contractID := int64(contract["id"].(float64))
	if cents := moneyStringCents(t, contract["amount"]); cents != 12800000 {
		t.Fatalf("有行项目时合同金额应为行项目合计 128000.00，实际 %v", contract["amount"])
	}
	if status, api := changeContractStatus(t, env, sales, contractID, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交合同审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api := changeContractStatus(t, env, sales, contractID, 3, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审批通过合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if st, api := getContractRaw(t, env, sales, contractID); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("审批后查合同：HTTP %d code %d", st, api.Code)
	}

	// 7. 建回款计划（到期日已过）并登记部分回款：计划保持「逾期」。
	plan := e2eCreateReceivablePlan(t, env, sales, contractID, "128000.00", time.Now().AddDate(0, 0, -1))
	planID := int64(plan["id"].(float64))
	record := e2eCreateReceivableRecord(t, env, sales, planID, "50000.00", time.Now().Format("2006-01-02"))
	if record["recordNo"] == "" {
		t.Fatalf("回款记录应发放 HK 编号：%v", record)
	}

	// 8. 仪表盘：4 卡片、漏斗、趋势均非零。
	data := fetchDashboardSummary(t, env, sales)
	cards := dashboardCards(t, data)
	if cards["customerCount"].(float64) < 1 {
		t.Fatalf("仪表盘客户数应 >= 1：%v", cards)
	}
	if moneyStringCents(t, cards["inProgressOpportunityAmount"]) <= 0 {
		t.Fatalf("进行中商机金额应非零：%v", cards)
	}
	if moneyStringCents(t, cards["signedContractAmount"]) <= 0 {
		t.Fatalf("已签合同金额应非零：%v", cards)
	}
	if cards["overdueReceivableCount"].(float64) < 1 ||
		moneyStringCents(t, cards["overdueReceivableAmount"]) <= 0 {
		t.Fatalf("逾期回款卡片应非零：%v", cards)
	}
	funnel := dashboardFunnelByStage(t, data)
	if funnel[2]["count"].(float64) < 1 || funnel[5]["count"].(float64) < 1 {
		t.Fatalf("漏斗应同时有阶段 2 与阶段 5 的商机：%v", funnel)
	}
	trend := dashboardTrendByMonth(t, data)
	month := time.Now().In(env.Config.Location()).Format("2006-01")
	point, ok := trend[month]
	if !ok {
		t.Fatalf("趋势应包含本月 %s：%v", month, trend)
	}
	if moneyStringCents(t, point["contractAmount"]) <= 0 || moneyStringCents(t, point["receiptAmount"]) <= 0 {
		t.Fatalf("本月合同/回款趋势应非零：%v", point)
	}

	// 9. 公海释放 → 公海可见 → 认领 → 负责人恢复。
	poolCustomer := createCustomer(t, env, sales, "端到端公海验收客户", nil)
	poolID := int64(poolCustomer["id"].(float64))
	if status, api := releaseCustomerRaw(t, env, sales, poolID, "验收释放公海"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("释放客户到公海：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	poolList, poolTotal := pagePoolCustomers(t, env, sales, "&name=端到端公海验收客户")
	if poolTotal != 1 || len(poolList) != 1 {
		t.Fatalf("公海列表应看到刚释放的客户：total=%d", poolTotal)
	}
	if poolList[0]["poolEnterTime"] == nil || poolList[0]["releaseReason"] != "验收释放公海" {
		t.Fatalf("释放后应落池时间与释放原因：%v", poolList[0])
	}
	if status, api := claimCustomerRaw(t, env, sales, poolID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("认领公海客户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if owner := ownerOf(t, env, sales, poolID); owner != ownerID {
		t.Fatalf("认领后负责人应为当前销售 %d，实际 %d", ownerID, owner)
	}
	histRows, histTotal := pageOwnerHistory(t, env, sales, poolID)
	if histTotal != 3 || histRows[0]["action"] != "claim" || histRows[1]["action"] != "release" {
		t.Fatalf("公海历史应为 create+release+claim，实际 %d 行：%v", histTotal, histRows)
	}

	// 10. 回收任务：有未作废合同不回收；无合同且超期回收。
	// 主客户已签合同：把最后跟进时间回填到 60 天前，仍应被合同排除。
	setCustomerTimes(t, env, customerID, ptrTime(daysAgo(60)), nil)
	stale := createCustomer(t, env, sales, "端到端超期无合同客户", nil)
	staleID := int64(stale["id"].(float64))
	setCustomerTimes(t, env, staleID, ptrTime(daysAgo(60)), nil)

	runRecycleJobAndWait(t, env, admin)

	if owner := ownerOf(t, env, admin, customerID); owner != ownerID {
		t.Fatalf("有未作废合同的客户不应被回收：期望 owner=%d，实际 %d", ownerID, owner)
	}
	if owner := ownerOf(t, env, admin, staleID); owner != 0 {
		t.Fatalf("无合同且超期未跟进的客户应被回收，实际 owner=%d", owner)
	}
	staleDetail := e2eCustomerDetail(t, env, admin, staleID)
	if staleDetail["poolEnterTime"] == nil || staleDetail["releaseReason"] == "" {
		t.Fatalf("回收后应落池时间与自动回收原因：%v", staleDetail)
	}
	staleHistory, _ := pageOwnerHistory(t, env, admin, staleID)
	if len(staleHistory) == 0 || staleHistory[0]["action"] != "recycle" {
		t.Fatalf("回收应落 recycle 历史，实际 %v", staleHistory)
	}
}

// e2eCreateProductCategory 新建产品分类并返回响应。
// 用本文件自带的助手（不复用 product_category_http_integration_test.go 的
// createProductCategory）：代码生成编译校验会剔除产品实体相关测试文件，
// 端到端验收用例需要独立于这组助手存在。
func e2eCreateProductCategory(t *testing.T, env *testutil.IntegrationEnv, token, name string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/product-category", token, map[string]any{"name": name})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("新建产品分类 %q：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析产品分类: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// e2eCreateProduct 新建产品并返回响应。
func e2eCreateProduct(t *testing.T, env *testutil.IntegrationEnv, token, name string, payload map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"name": name}
	for k, v := range payload {
		body[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/product", token, body)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("新建产品 %q：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析产品: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// e2eCustomerDetail 查询客户详情并返回 data。
func e2eCustomerDetail(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) map[string]any {
	t.Helper()
	status, api := getCustomer(t, env, token, id)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询客户 %d 详情：HTTP %d code %d msg %q", id, status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析客户详情: %v（data=%s）", err, string(api.Data))
	}
	return detail
}

// e2eCreateReceivablePlan 新建回款计划并返回响应。
func e2eCreateReceivablePlan(t *testing.T, env *testutil.IntegrationEnv, token string, contractID int64, amount string, dueDate time.Time) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/receivable/plan", token, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": amount,
		"dueDate": dueDate.Format("2006-01-02"), "remark": "端到端验收首期",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("新建回款计划：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析回款计划: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// e2eCreateReceivableRecord 登记回款记录并返回响应。
func e2eCreateReceivableRecord(t *testing.T, env *testutil.IntegrationEnv, token string, planID int64, amount, receiptDate string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/receivable/record", token, map[string]any{
		"planId": planID, "amount": amount, "receiptDate": receiptDate,
		"method": "银行转账", "remark": "端到端验收回款",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("登记回款记录：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析回款记录: %v（data=%s）", err, string(api.Data))
	}
	return row
}
