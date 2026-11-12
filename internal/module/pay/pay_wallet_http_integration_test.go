//go:build integration

// pay 钱包域 HTTP/服务级集成测试（issue #132，spec §4.3/§8.2）：
//   - 唯一入账入口（服务级直调，本票尚无业务流消费方）：并发双扣一成一 409、
//     懒创建并发单钱包行、余额不足 409 文案（含当前余额/变更量）、流水快照
//     与重放代数和一致、LS 单号格式；
//   - 自助端点（只挂登录、本人过滤）、管理端分页权限 403、租户隔离。
package pay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// walletCtx 构造带登录主体与租户的服务级上下文（同 system 中间件双注入口径）。
func walletCtx(userID, tenantID int64) context.Context {
	ctx := authn.With(context.Background(), authn.Principal{UserID: userID, TenantID: tenantID})
	return tenant.WithTenantID(ctx, tenantID)
}

// applyWallet 直调唯一入账入口，返回 (流水, 错误)。
func applyWallet(env *testutil.IntegrationEnv, userID, tenantID int64, ch payservice.WalletChange) (*model.WalletTransaction, error) {
	return env.App.Pay.ApplyWalletChange(walletCtx(userID, tenantID), ch)
}

// walletStatus 提取入账错误的业务状态码（nil 错误 → 0）。
func walletStatus(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := xerr.As(err); ok {
		return e.Status
	}
	return -1
}

// pageWallets 经管理端分页查询钱包，返回原始 JSON（用例自行断言）。
func pageWallets(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, testutil.APIResponse, []map[string]any) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/pay/wallet/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("钱包分页：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		Total int64            `json:"total"`
		List  []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析钱包分页: %v（data=%s）", err, api.Data)
	}
	return status, api, page.List
}

// findWalletByUser 在管理端分页里按 userId 过滤出唯一钱包（不存在 Fatal）。
func findWalletByUser(t *testing.T, env *testutil.IntegrationEnv, token string, userID int64) map[string]any {
	t.Helper()
	_, _, list := pageWallets(t, env, token, fmt.Sprintf("&userId=%d", userID))
	if len(list) != 1 {
		t.Fatalf("userId=%d 应恰有 1 个钱包，实际 %d 个", userID, len(list))
	}
	return list[0]
}

// walletField 断言钱包 JSON 字段为期望的十进制金额字符串（money 经 MarshalJSON
// 输出字符串，全程不经过 float64）。
func walletAmount(t *testing.T, row map[string]any, key, want string) {
	t.Helper()
	got, _ := row[key].(string)
	if got != want {
		t.Fatalf("钱包 %s = %v，期望 %q", key, row[key], want)
	}
}

// pageWalletTransactions 经管理端分页查询流水并返回条目（id DESC）。
func pageWalletTransactions(t *testing.T, env *testutil.IntegrationEnv, token, query string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/pay/wallet-transaction/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("流水分页：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		Total int64            `json:"total"`
		List  []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析流水分页: %v（data=%s）", err, api.Data)
	}
	if page.Total != int64(len(page.List)) {
		t.Fatalf("流水 total=%d 与 list 长度 %d 不一致（pageSize=50 应全覆盖）", page.Total, len(page.List))
	}
	return page.List
}

// TestIntegrationPayWalletConcurrentDoubleDeduct 并发双扣（spec §8.2 钱包核心
// 场景）：两请求同钱包并发扣款，一成一 409，无负余额。唯一入账入口本票尚无
// 业务流消费方（支付是 #133），按票面以服务级直调验证。
func TestIntegrationPayWalletConcurrentDoubleDeduct(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 准备：充值 +100.00（顺带懒创建钱包）。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "并发双扣准备充值", Price: money.FromCents(10_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}

	// 并发双扣：两个 -60.00（余额只够一笔）。
	const goroutines = 2
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = applyWallet(env, 1, 1, payservice.WalletChange{
				UserID: 1, UserType: model.WalletUserTypeAdmin,
				BizType: model.WalletBizPayment, Title: fmt.Sprintf("并发扣款 %d", i), Price: money.FromCents(-6_000),
			})
		}(i)
	}
	wg.Wait()

	statuses := []int{walletStatus(errs[0]), walletStatus(errs[1])}
	sorted := append([]int(nil), statuses...)
	if sorted[0] > sorted[1] {
		sorted[0], sorted[1] = sorted[1], sorted[0]
	}
	if sorted[0] != 0 || sorted[1] != http.StatusConflict {
		t.Fatalf("并发双扣应一成(0)一 409，实际 %v（err=%v/%v）", statuses, errs[0], errs[1])
	}

	// 管理端核对：余额 40.00、无负余额；统计列 100/60。
	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "40.00")
	walletAmount(t, row, "totalRecharge", "100.00")
	walletAmount(t, row, "totalExpense", "60.00")

	// 失败者不留流水：恰 2 笔（充值 1 + 成功扣款 1）。
	walletID := int64(row["id"].(float64))
	txs := pageWalletTransactions(t, env, admin, fmt.Sprintf("&walletId=%d", walletID))
	if len(txs) != 2 {
		t.Fatalf("并发双扣后应恰 2 笔流水，实际 %d", len(txs))
	}
}

// TestIntegrationPayWalletConcurrentLazyCreate 懒创建并发（spec §4.3/§8.2）：
// 两请求同触发首笔入账，UNIQUE 兜底 + 冲突重读，只产生一行钱包、两笔都成。
func TestIntegrationPayWalletConcurrentLazyCreate(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	const freshUser = int64(101)

	const goroutines = 2
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = applyWallet(env, freshUser, 1, payservice.WalletChange{
				UserID: freshUser, UserType: model.WalletUserTypeAdmin,
				BizType: model.WalletBizRecharge, Title: fmt.Sprintf("并发首触充值 %d", i), Price: money.FromCents(1_000),
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发首触 %d 应都成功，实际: %v", i, err)
		}
	}

	row := findWalletByUser(t, env, admin, freshUser)
	walletAmount(t, row, "balance", "20.00")
}

// TestIntegrationPayWalletInsufficientBalance409 余额不足 409（spec §4.3）：
// 文案含当前余额与变更量；失败不留流水、余额不变。
func TestIntegrationPayWalletInsufficientBalance409(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "余额不足准备", Price: money.FromCents(3_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}

	_, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizPayment, Title: "超额扣款", Price: money.FromCents(-5_000),
	})
	if walletStatus(err) != http.StatusConflict {
		t.Fatalf("余额不足应 409，实际 %v（err=%v）", walletStatus(err), err)
	}
	for _, want := range []string{"余额不足", "30.00", "50.00"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("409 文案 %q 应含 %q", err.Error(), want)
		}
	}

	// 失败不留痕：恰 1 笔流水、余额仍 30.00。
	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "30.00")
	walletID := int64(row["id"].(float64))
	if txs := pageWalletTransactions(t, env, admin, fmt.Sprintf("&walletId=%d", walletID)); len(txs) != 1 {
		t.Fatalf("失败的入账不应留流水，实际 %d 笔", len(txs))
	}
}

// TestIntegrationPayWalletSnapshotReplay 快照一致性（spec §8.2/§9：快照可重放）：
// 混合出入系列后，逐笔快照与手工推演的代数和一致（测试内硬编码期望值，
// 不复算代码逻辑）；最终余额 = 末笔快照；LS 单号格式符合 spec §4.9。
func TestIntegrationPayWalletSnapshotReplay(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 序列（五类可用 biz_type 混合；5 余额调整预留被拒，纯函数单测覆盖）：
	// 期望快照为手工推演值。
	series := []struct {
		bizType  int8
		title    string
		price    int64
		snapshot string
	}{
		{model.WalletBizRecharge, "充值一", 10_000, "100.00"},
		{model.WalletBizPayment, "支付一", -3_000, "70.00"},
		{model.WalletBizPaymentRefund, "支付退款一", 2_000, "90.00"},
		{model.WalletBizRechargeRefund, "充值退款一", -1_500, "75.00"},
		{model.WalletBizTransfer, "转入一", 2_500, "100.00"},
		{model.WalletBizPayment, "支付二", -10_000, "0.00"},
	}
	for i, step := range series {
		if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
			UserID: 1, UserType: model.WalletUserTypeAdmin,
			BizType: step.bizType, Title: step.title, Price: money.FromCents(step.price),
		}); err != nil {
			t.Fatalf("第 %d 步 %s: %v", i+1, step.title, err)
		}
	}

	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "0.00")
	walletAmount(t, row, "totalRecharge", "85.00")
	walletAmount(t, row, "totalExpense", "110.00")

	walletID := int64(row["id"].(float64))
	txs := pageWalletTransactions(t, env, admin, fmt.Sprintf("&walletId=%d", walletID))
	if len(txs) != len(series) {
		t.Fatalf("应恰 %d 笔流水，实际 %d", len(series), len(txs))
	}
	noPattern := regexp.MustCompile(`^LS\d{8}\d{4,}$`)
	// 分页固定 id DESC：倒序回放为入账顺序。
	for i := len(series) - 1; i >= 0; i-- {
		tx := txs[len(txs)-1-i]
		want := series[i]
		if got, _ := tx["balance"].(string); got != want.snapshot {
			t.Fatalf("第 %d 笔 %s 快照 balance=%v，期望 %q", i+1, want.title, tx["balance"], want.snapshot)
		}
		if got, _ := tx["price"].(string); got != money.FromCents(want.price).String() {
			t.Fatalf("第 %d 笔 %s price=%v，期望 %q", i+1, want.title, tx["price"], money.FromCents(want.price).String())
		}
		if no, _ := tx["no"].(string); !noPattern.MatchString(no) {
			t.Fatalf("流水单号 %q 不符合 LS 编号规则（spec §4.9）", no)
		}
	}
}

// TestIntegrationPayWalletMyEndpoints 自助端点（spec §4.11）：只挂登录（无 token
// 401）、user_id 服务层强制过滤（demo 看不到 admin 的钱包与流水）。
func TestIntegrationPayWalletMyEndpoints(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")

	// 未登录 401。
	status, _ := env.Request(t, http.MethodGet, "/pay/wallet/my", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录查我的钱包应 401，实际 HTTP %d", status)
	}

	// admin 首查懒创建（余额 0.00），给 admin 钱包入账两笔。
	status, api := env.Request(t, http.MethodGet, "/pay/wallet/my", admin, nil)
	mustOK(t, "我的钱包首查", status, api)
	var myWallet struct {
		ID      int64  `json:"id"`
		UserID  int64  `json:"userId"`
		Balance string `json:"balance"`
	}
	if err := json.Unmarshal(api.Data, &myWallet); err != nil {
		t.Fatalf("解析我的钱包: %v（data=%s）", err, api.Data)
	}
	if myWallet.UserID != 1 || myWallet.Balance != "0.00" {
		t.Fatalf("懒创建钱包异常：%s", api.Data)
	}
	for _, ch := range []payservice.WalletChange{
		{UserID: 1, UserType: model.WalletUserTypeAdmin, BizType: model.WalletBizRecharge, Title: "我的充值", Price: money.FromCents(5_000)},
		{UserID: 1, UserType: model.WalletUserTypeAdmin, BizType: model.WalletBizPayment, Title: "我的支付", Price: money.FromCents(-2_000)},
	} {
		if _, err := env.App.Pay.ApplyWalletChange(walletCtx(1, 1), ch); err != nil {
			t.Fatalf("入账 %s: %v", ch.Title, err)
		}
	}

	// demo 首查懒创建自己的钱包：userId 不同，余额独立。
	status, api = env.Request(t, http.MethodGet, "/pay/wallet/my", demo, nil)
	mustOK(t, "demo 我的钱包首查", status, api)
	var demoWallet struct {
		ID     int64 `json:"id"`
		UserID int64 `json:"userId"`
	}
	if err := json.Unmarshal(api.Data, &demoWallet); err != nil {
		t.Fatalf("解析 demo 我的钱包: %v", err)
	}
	if demoWallet.UserID != 2 || demoWallet.ID == myWallet.ID {
		t.Fatalf("demo 应拿到自己的钱包：%s", api.Data)
	}

	// 我的流水：admin 2 笔；demo 0 笔（归属过滤，看不到 admin 的流水）。
	status, api = env.Request(t, http.MethodGet, "/pay/wallet-transaction/my?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "admin 我的流水", status, api)
	var adminTxs struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &adminTxs); err != nil || adminTxs.Total != 2 {
		t.Fatalf("admin 我的流水应 2 笔，实际 %s（err=%v）", api.Data, err)
	}
	status, api = env.Request(t, http.MethodGet, "/pay/wallet-transaction/my?pageNo=1&pageSize=10", demo, nil)
	mustOK(t, "demo 我的流水", status, api)
	var demoTxs struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &demoTxs); err != nil || demoTxs.Total != 0 {
		t.Fatalf("demo 我的流水应 0 笔（非本人过滤），实际 %s（err=%v）", api.Data, err)
	}

	// 请求里的 walletId 不采纳：demo 指定 admin 钱包 ID 也只能看到自己的（空）。
	status, api = env.Request(t, http.MethodGet,
		fmt.Sprintf("/pay/wallet-transaction/my?pageNo=1&pageSize=10&walletId=%d", myWallet.ID), demo, nil)
	mustOK(t, "demo 越权 walletId", status, api)
	if err := json.Unmarshal(api.Data, &demoTxs); err != nil || demoTxs.Total != 0 {
		t.Fatalf("demo 指定他人 walletId 应仍被归属过滤，实际 %s", api.Data)
	}
}

// TestIntegrationPayWalletAdminPagePermission 管理端权限（spec §4.11/§7）：
// 普通 demo 角色 403；平台 admin 可查钱包与流水分页。
func TestIntegrationPayWalletAdminPagePermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "管理页准备充值", Price: money.FromCents(1_00),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}

	for _, path := range []string{"/pay/wallet/page", "/pay/wallet-transaction/page"} {
		status, _ := env.Request(t, http.MethodGet, path+"?pageNo=1&pageSize=10", demo, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色查 %s 应 403，实际 HTTP %d", path, status)
		}
	}

	// admin：钱包分页可见 userId=1 的钱包；流水分页可查。
	_, _, list := pageWallets(t, env, admin, "")
	if len(list) != 1 {
		t.Fatalf("admin 钱包分页应 1 行，实际 %d", len(list))
	}
	walletID := int64(list[0]["id"].(float64))
	txs := pageWalletTransactions(t, env, admin, fmt.Sprintf("&walletId=%d", walletID))
	if len(txs) != 1 {
		t.Fatalf("流水抽屉数据应 1 笔，实际 %d", len(txs))
	}
}

// TestIntegrationPayWalletTenantIsolation 租户隔离（spec §4.11/§8.2）：
// 两租户同 user_id 各自懒创建独立钱包；分页互不可见；跨租户 walletId 过滤
// 查不到任何流水。
func TestIntegrationPayWalletTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "租户 1 充值", Price: money.FromCents(7_00),
	}); err != nil {
		t.Fatalf("租户 1 充值: %v", err)
	}
	tenant1Wallet := findWalletByUser(t, env, admin, 1)
	wallet1ID := int64(tenant1Wallet["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "钱包隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	if _, err := applyWallet(env, 1, tenant2, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "租户 2 充值", Price: money.FromCents(9_00),
	}); err != nil {
		t.Fatalf("租户 2 充值: %v", err)
	}

	// 租户 2 只见自己的钱包（同 user_id=1，不同租户行不同 ID）。
	tenant2Wallet := findWalletByUser(t, env, token2, 1)
	wallet2ID := int64(tenant2Wallet["id"].(float64))
	if wallet1ID == wallet2ID {
		t.Fatalf("两租户钱包 ID 不应相同（%d）", wallet1ID)
	}
	walletAmount(t, tenant2Wallet, "balance", "9.00")
	walletAmount(t, tenant1Wallet, "balance", "7.00")

	// 跨租户 walletId 过滤：租户 2 查租户 1 钱包的流水 → 空；反之亦然。
	if txs := pageWalletTransactions(t, env, token2, fmt.Sprintf("&walletId=%d", wallet1ID)); len(txs) != 0 {
		t.Fatalf("租户 2 不应查到租户 1 钱包流水，实际 %d 笔", len(txs))
	}
	if txs := pageWalletTransactions(t, env, admin, fmt.Sprintf("&walletId=%d", wallet2ID)); len(txs) != 0 {
		t.Fatalf("租户 1 不应查到租户 2 钱包流水，实际 %d 笔", len(txs))
	}
}
