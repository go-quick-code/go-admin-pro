//go:build integration

// CRM 演示数据集成测试（issue #21）：真实 MySQL 上验证
//   - 一条命令（seed.Run(db, true)）写入完整演示组织与业务数据；
//   - 重复执行幂等，不重复插入；
//   - 演示账号可登录，数据范围（仅本人 / 本部门及以下）按角色生效；
//   - 演示数据覆盖仪表盘所需的客户状态、商机阶段、合同状态、回款派生状态
//     与近 12 个月的时间分布；
//   - 客户/商机/合同/回款编号与 crm_sequence 同步，后续 UI 新建不会撞号。
package seed_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// demoCounts 是一次演示数据写入后的行数快照（幂等断言用）。
type demoCounts struct {
	Customers          int64
	Contacts           int64
	FollowUps          int64
	Opportunities      int64
	OpportunityProduct int64
	Contracts          int64
	ContractProduct    int64
	ReceivablePlans    int64
	ReceivableRecords  int64
	OwnerHistories     int64
	Categories         int64
	Products           int64
	Sequences          int64
	Roles              int64
	Users              int64
}

func countDemo(t *testing.T, db *gorm.DB) demoCounts {
	t.Helper()
	count := func(model any) int64 {
		var total int64
		if err := tenant.Skip(db).Model(model).Count(&total).Error; err != nil {
			t.Fatalf("统计 %T: %v", model, err)
		}
		return total
	}
	return demoCounts{
		Customers:          count(&crmmodel.Customer{}),
		Contacts:           count(&crmmodel.Contact{}),
		FollowUps:          count(&crmmodel.FollowUp{}),
		Opportunities:      count(&crmmodel.Opportunity{}),
		OpportunityProduct: count(&crmmodel.OpportunityProduct{}),
		Contracts:          count(&crmmodel.Contract{}),
		ContractProduct:    count(&crmmodel.ContractProduct{}),
		ReceivablePlans:    count(&crmmodel.ReceivablePlan{}),
		ReceivableRecords:  count(&crmmodel.ReceivableRecord{}),
		OwnerHistories:     count(&crmmodel.CustomerOwnerHistory{}),
		Categories:         count(&crmmodel.ProductCategory{}),
		Products:           count(&crmmodel.Product{}),
		Sequences:          count(&crmmodel.Sequence{}),
		Roles:              count(&sysmodel.Role{}),
		Users:              count(&sysmodel.User{}),
	}
}

// TestIntegrationCRMDemoSeed 验证演示数据的完整性、幂等性与数据范围。
func TestIntegrationCRMDemoSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 测试基座默认关闭演示数据；显式写入第一遍。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("写入 CRM 演示数据: %v", err)
	}
	first := countDemo(t, env.DB)
	want := demoCounts{
		Customers: 8, Contacts: 12, FollowUps: 9,
		Opportunities: 6, OpportunityProduct: 5,
		Contracts: 6, ContractProduct: 6,
		ReceivablePlans: 6, ReceivableRecords: 5,
		OwnerHistories: 10, Categories: 2, Products: 4,
		Roles: 4, Users: 5,
	}
	if first.Customers != want.Customers || first.Contacts != want.Contacts ||
		first.FollowUps != want.FollowUps || first.Opportunities != want.Opportunities ||
		first.OpportunityProduct != want.OpportunityProduct ||
		first.Contracts != want.Contracts || first.ContractProduct != want.ContractProduct ||
		first.ReceivablePlans != want.ReceivablePlans || first.ReceivableRecords != want.ReceivableRecords ||
		first.OwnerHistories != want.OwnerHistories || first.Categories != want.Categories ||
		first.Products != want.Products || first.Roles != want.Roles || first.Users != want.Users {
		t.Fatalf("演示数据行数不符：\n got %+v\nwant %+v", first, want)
	}
	if first.Sequences == 0 {
		t.Fatal("演示数据应同步写入 crm_sequence")
	}

	// 重复执行必须整段跳过：计数完全不变。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("重复写入 CRM 演示数据: %v", err)
	}
	second := countDemo(t, env.DB)
	if second != first {
		t.Fatalf("重复 seed 不应新增行：\n before %+v\n after %+v", first, second)
	}

	// 演示账号可登录。
	sales1 := env.Login(t, "sales1", "123456")
	sales2 := env.Login(t, "sales2", "123456")
	manager := env.Login(t, "manager", "123456")

	assertPageTotal(t, env, admin, "/crm/customer/page", 6, "admin 普通客户列表（排除 2 个公海客户）")
	assertPageTotal(t, env, sales1, "/crm/customer/page", 2, "sales1 仅本人客户")
	assertPageTotal(t, env, sales2, "/crm/customer/page", 1, "sales2 仅本人客户")
	assertPageTotal(t, env, manager, "/crm/customer/page", 4, "销售主管本部门及以下客户")
	assertPageTotal(t, env, admin, "/crm/customer/pool/page", 2, "公海客户")

	// 销售只看到自己的客户：行上的 ownerUserId 必须等于 sales1。
	var sales1User sysmodel.User
	if err := tenant.Skip(env.DB).Where("tenant_id = ? AND username = ?", 1, "sales1").First(&sales1User).Error; err != nil {
		t.Fatalf("读取演示用户 sales1: %v", err)
	}
	list := pageList(t, env, sales1, "/crm/customer/page")
	for _, row := range list {
		if row["ownerUserId"] != float64(sales1User.ID) {
			t.Fatalf("sales1 仅本人范围下出现他人客户：%v", row)
		}
	}

	// 业务状态覆盖：客户 3 态、商机 6 阶、合同 4 态、回款 4 种派生状态。
	assertDistinct(t, env.DB, &crmmodel.Customer{}, "status", 3, "客户状态")
	assertDistinct(t, env.DB, &crmmodel.Opportunity{}, "stage", 6, "商机阶段")
	assertDistinct(t, env.DB, &crmmodel.Contract{}, "status", 4, "合同状态")
	assertReceivableStatuses(t, env.DB)

	// 时间分布：有近 30 天跟进与超期未跟进客户；已签合同与回款记录覆盖 >= 3 个月。
	var recentFollow, staleFollow int64
	cutoff := time.Now().AddDate(0, 0, -30)
	if err := tenant.Skip(env.DB).Model(&crmmodel.Customer{}).
		Where("last_follow_time >= ?", cutoff).Count(&recentFollow).Error; err != nil {
		t.Fatalf("统计近期跟进客户: %v", err)
	}
	if err := tenant.Skip(env.DB).Model(&crmmodel.Customer{}).
		Where("last_follow_time IS NOT NULL AND last_follow_time < ?", cutoff).Count(&staleFollow).Error; err != nil {
		t.Fatalf("统计超期未跟进客户: %v", err)
	}
	if recentFollow == 0 || staleFollow == 0 {
		t.Fatalf("客户跟进时间应有近有远：近 30 天 %d，超期 %d", recentFollow, staleFollow)
	}

	// 仪表盘：4 卡片、漏斗、趋势均有非零数据。
	data := fetchDashboard(t, env, admin)
	cards := data["cards"].(map[string]any)
	if cards["customerCount"] != float64(8) {
		t.Fatalf("仪表盘客户数应为 8（含公海），实际 %v", cards["customerCount"])
	}
	if moneyCents(t, cards["inProgressOpportunityAmount"]) <= 0 ||
		moneyCents(t, cards["signedContractAmount"]) <= 0 ||
		moneyCents(t, cards["overdueReceivableAmount"]) <= 0 ||
		cards["overdueReceivableCount"].(float64) <= 0 {
		t.Fatalf("仪表盘卡片应非零：%v", cards)
	}
	funnel := data["funnel"].([]any)
	if len(funnel) != 6 {
		t.Fatalf("漏斗应固定 6 档，实际 %d", len(funnel))
	}
	var won, lost bool
	for _, item := range funnel {
		row := item.(map[string]any)
		if row["stage"] == float64(5) && row["count"].(float64) > 0 {
			won = true
		}
		if row["stage"] == float64(6) && row["count"].(float64) > 0 {
			lost = true
		}
	}
	if !won || !lost {
		t.Fatalf("漏斗应同时有赢单与输单：%v", funnel)
	}
	trend := data["trend"].([]any)
	var contractMonths, receiptMonths int
	for _, item := range trend {
		row := item.(map[string]any)
		if moneyCents(t, row["contractAmount"]) > 0 {
			contractMonths++
		}
		if moneyCents(t, row["receiptAmount"]) > 0 {
			receiptMonths++
		}
	}
	if contractMonths < 3 || receiptMonths < 3 {
		t.Fatalf("已签合同/回款应覆盖至少 3 个月，实际合同 %d 个月、回款 %d 个月", contractMonths, receiptMonths)
	}

	// 编号与序列一致：演示编号的日期键 + 序号必须被 crm_sequence 覆盖。
	assertCustomerSequences(t, env.DB)
	assertSequenceForModel(t, env.DB, &crmmodel.Opportunity{}, "opportunity", "opportunity_no")
	assertSequenceForModel(t, env.DB, &crmmodel.Contract{}, "contract", "contract_no")
	assertSequenceForModel(t, env.DB, &crmmodel.ReceivableRecord{}, "receivable_record", "record_no")

	// 通过 UI 新建客户时，编号从序列继续发放，不与演示编号冲突。
	created := createCustomerViaAPI(t, env, sales1, "演示序列续号客户")
	no, _ := created["customerNo"].(string)
	if no == "" {
		t.Fatalf("新建客户编号为空：%v", created)
	}
	if duplicateNumber(t, env.DB, &crmmodel.Customer{}, "customer_no", no) > 1 {
		t.Fatalf("新建客户编号 %q 与演示数据重复", no)
	}
}

// assertPageTotal 查询分页接口并断言 total。
func assertPageTotal(t *testing.T, env *testutil.IntegrationEnv, token, path string, want int64, label string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, path+"?pageNo=1&pageSize=50", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q", label, status, api.Code, api.Msg)
	}
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析 %s: %v（data=%s）", label, err, string(api.Data))
	}
	if page.Total != want {
		t.Fatalf("%s total=%d，期望 %d", label, page.Total, want)
	}
}

// pageList 返回分页接口的 list。
func pageList(t *testing.T, env *testutil.IntegrationEnv, token, path string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, path+"?pageNo=1&pageSize=50", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 %s：HTTP %d code %d msg %q", path, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析 %s: %v（data=%s）", path, err, string(api.Data))
	}
	return page.List
}

// assertDistinct 断言某列的不同取值数量。
func assertDistinct(t *testing.T, db *gorm.DB, model any, column string, want int64, label string) {
	t.Helper()
	var got int64
	if err := tenant.Skip(db).Model(model).Distinct(column).Count(&got).Error; err != nil {
		t.Fatalf("统计%s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s应覆盖 %d 种取值，实际 %d", label, want, got)
	}
}

// assertReceivableStatuses 用与 service 相同的派生口径统计四种计划状态，断言各至少 1 条。
func assertReceivableStatuses(t *testing.T, db *gorm.DB) {
	t.Helper()
	today := time.Now().Format("2006-01-02")
	var row struct {
		Received int64 `gorm:"column:received"`
		Partial  int64 `gorm:"column:partial"`
		Pending  int64 `gorm:"column:pending"`
		Overdue  int64 `gorm:"column:overdue"`
	}
	err := tenant.Skip(db).Raw(`
		SELECT
			COALESCE(SUM(received >= amount), 0) AS received,
			COALESCE(SUM(received > 0 AND received < amount AND due_date >= ?), 0) AS partial,
			COALESCE(SUM(received = 0 AND due_date >= ?), 0) AS pending,
			COALESCE(SUM(received < amount AND due_date < ?), 0) AS overdue
		FROM (
			SELECT p.amount, p.due_date,
				COALESCE((SELECT SUM(r.amount) FROM crm_receivable_record r
					WHERE r.plan_id = p.id AND r.deleted_at IS NULL), 0) AS received
			FROM crm_receivable_plan p WHERE p.deleted_at IS NULL
		) t`, today, today, today).Scan(&row).Error
	if err != nil {
		t.Fatalf("统计回款计划派生状态: %v", err)
	}
	if row.Received == 0 || row.Partial == 0 || row.Pending == 0 || row.Overdue == 0 {
		t.Fatalf("回款计划应覆盖已回款/部分回款/待回款/逾期：%+v", row)
	}
}

// fetchDashboard 调用仪表盘接口并返回 data。
func fetchDashboard(t *testing.T, env *testutil.IntegrationEnv, token string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/dashboard/summary", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询仪表盘：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析仪表盘: %v（data=%s）", err, string(api.Data))
	}
	return data
}

// moneyCents 把响应中的金额字符串解析为分。
func moneyCents(t *testing.T, value any) int64 {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("金额应为十进制字符串，实际 %T（%v）", value, value)
	}
	parsed, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("金额 %q 解析失败: %v", text, err)
	}
	return parsed.Cents()
}

// assertCustomerSequences 校验所有演示客户编号的日期键/序号都被 crm_sequence 覆盖。
func assertCustomerSequences(t *testing.T, db *gorm.DB) {
	t.Helper()
	var customers []crmmodel.Customer
	if err := tenant.Skip(db).Find(&customers).Error; err != nil {
		t.Fatalf("读取演示客户: %v", err)
	}
	for _, c := range customers {
		assertSequenceCovers(t, db, "customer", c.CustomerNo)
	}
}

// assertSequenceForModel 校验某实体编号列的序列覆盖。
func assertSequenceForModel(t *testing.T, db *gorm.DB, model any, bizType, column string) {
	t.Helper()
	var values []string
	if err := tenant.Skip(db).Model(model).Pluck(column, &values).Error; err != nil {
		t.Fatalf("读取 %s 编号: %v", bizType, err)
	}
	for _, no := range values {
		if no != "" {
			assertSequenceCovers(t, db, bizType, no)
		}
	}
}

// assertSequenceCovers 解析编号（前缀 2 字符 + 8 位日期 + 序号）并断言序列行存在且 >= 序号。
func assertSequenceCovers(t *testing.T, db *gorm.DB, bizType, no string) {
	t.Helper()
	if len(no) < 14 {
		t.Fatalf("%s 编号 %q 长度异常", bizType, no)
	}
	dateKey := no[2:10]
	seq, err := strconv.ParseInt(no[10:], 10, 64)
	if err != nil {
		t.Fatalf("%s 编号 %q 序号解析失败: %v", bizType, no, err)
	}
	var row crmmodel.Sequence
	err = tenant.Skip(db).
		Where("tenant_id = ? AND biz_type = ? AND date_key = ?", 1, bizType, dateKey).
		First(&row).Error
	if err != nil {
		t.Fatalf("%s 编号 %q 缺少 crm_sequence(%s): %v", bizType, no, dateKey, err)
	}
	if row.LastValue < seq {
		t.Fatalf("%s 编号 %q 的序列 last_value=%d 小于已用序号 %d", bizType, no, row.LastValue, seq)
	}
}

// createCustomerViaAPI 用指定 token 建客户并返回响应。
func createCustomerViaAPI(t *testing.T, env *testutil.IntegrationEnv, token, name string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/customer", token, map[string]any{"name": name})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建客户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建客户响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// duplicateNumber 统计指定表编号列等于 no 的行数。
func duplicateNumber(t *testing.T, db *gorm.DB, model any, column, no string) int64 {
	t.Helper()
	var total int64
	if err := tenant.Skip(db).Model(model).Where(column+" = ?", no).Count(&total).Error; err != nil {
		t.Fatalf("统计编号重复 %s=%s: %v", column, no, err)
	}
	return total
}
