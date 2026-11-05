//go:build integration

// PR #39 安全审查 P1 的锁序回归用例（合同作废 / 回款登记 / 编辑与审批并发）：
//   - 作废与登记回款统一「先合同、后计划」锁序：作废守卫在合同行锁内复核回款
//     记录，登记回款在锁内复核合同状态，最终不存在「已作废合同 + 新增回款」；
//   - 编辑与审批都在事务内先锁合同并复核最新状态：审批通过后金额/行项目不得
//     被基于旧状态（待审）的编辑写回。
//
// 锁序用可控阻塞点确定性地制造竞态（阻塞守卫 / 阻塞审批实现）：两个请求在
// 同一条合同行上排队，避免依赖随机调度窗口。
package crm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	crmrepo "github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	crmservice "github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
	systemrepo "github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// newContractTestService 用集成环境同一 DB 装配一个 crm Service（真实仓储 +
// 真实 system 网关），返回服务与平台管理员用户 ID。用于注入可控阻塞的审批/
// 守卫实现，在服务公共 seam 上验证事务锁序。
func newContractTestService(t *testing.T, env *testutil.IntegrationEnv) (*crmservice.Service, int64) {
	t.Helper()
	jwtManager := jwt.NewManager(env.Config.JWT.Secret, time.Hour, "test")
	sysSvc := systemservice.New(systemrepo.New(env.DB), jwtManager, env.Config)
	crmSvc := crmservice.New(crmrepo.New(env.DB), sysSvc)
	crmSvc.SetLocation(env.Config.Location())

	tenantCtx := tenant.WithTenantID(context.Background(), 1)
	admin, err := sysSvc.Repo().FindUserByUsername(tenantCtx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("查询平台管理员: err=%v", err)
	}
	return crmSvc, admin.ID
}

// platformCtx 构造平台租户管理员的认证上下文（服务层 CurrentUserID 与数据范围依赖）。
func platformCtx(userID int64) context.Context {
	return authn.With(tenant.WithTenantID(context.Background(), 1),
		authn.Principal{UserID: userID, TenantID: 1, Username: "admin"})
}

// blockingVoidGuard 是可阻塞的作废守卫：CanVoidContract 视为「已复核通过」后
// 暂停在返回前（旧实现无锁，此刻回款登记还能插入），CanVoidContractTx 在合同
// 行锁内暂停（新实现，登记回款会在锁上排队）。
type blockingVoidGuard struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingVoidGuard() *blockingVoidGuard {
	return &blockingVoidGuard{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *blockingVoidGuard) pause() {
	g.once.Do(func() { close(g.entered) })
	<-g.release
}

// CanVoidContract 实现 VoidContractGuard（无事务版本，仅旧实现会走到这里）。
func (g *blockingVoidGuard) CanVoidContract(context.Context, int64) error {
	g.pause()
	return nil
}

// CanVoidContractTx 实现事务版守卫（新实现，在合同行锁内调用）。
func (g *blockingVoidGuard) CanVoidContractTx(context.Context, *crmrepo.Repository, int64) error {
	g.pause()
	return nil
}

// blockingApproval 是可阻塞的审批实现：Approve 在持有合同行锁的事务里暂停，
// 让并发编辑请求在锁上排队；释放后按最新状态条件更新为已签。
type blockingApproval struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingApproval() *blockingApproval {
	return &blockingApproval{entered: make(chan struct{}), release: make(chan struct{})}
}

func transitionApprovalTx(ctx context.Context, tx *crmrepo.Repository, c *crmmodel.Contract, to int8, reason string) error {
	rows, err := tx.TransitionContractStatus(ctx, c.ID, c.Status, to, reason)
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("合同状态已被并发变更")
	}
	c.Status = to
	return nil
}

func (a *blockingApproval) Submit(ctx context.Context, tx *crmrepo.Repository, c *crmmodel.Contract) error {
	return transitionApprovalTx(ctx, tx, c, crmmodel.ContractStatusPending, "")
}

func (a *blockingApproval) Approve(ctx context.Context, tx *crmrepo.Repository, c *crmmodel.Contract) error {
	close(a.entered)
	<-a.release
	return transitionApprovalTx(ctx, tx, c, crmmodel.ContractStatusSigned, "")
}

func (a *blockingApproval) Reject(ctx context.Context, tx *crmrepo.Repository, c *crmmodel.Contract, reason string) error {
	return transitionApprovalTx(ctx, tx, c, crmmodel.ContractStatusDraft, reason)
}

// waitResult 等待 goroutine 结果或超时（避免死锁把测试挂住）。
func waitResult(t *testing.T, what string, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("等待 %s 超时（疑似死锁）", what)
		return nil
	}
}

// TestIntegrationContractVoidReceivableSerialized P1：作废合同与登记回款并发时，
// 两者在合同行锁上串行化——作废先拿到锁时，登记回款在锁上等待并在作废提交后
// 读到作废态被拒绝；不会出现「合同已作废 + 新增回款记录」。
func TestIntegrationContractVoidReceivableSerialized(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "P1 作废并发客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "P1 作废并发合同", map[string]any{
		"customerId": customerID, "amount": "100.00",
	})
	contractID := int64(contract["id"].(float64))
	if st, api := changeContractStatus(t, env, admin, contractID, 2, ""); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	if st, api := changeContractStatus(t, env, admin, contractID, 3, ""); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("审批通过：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "50.00", "dueDate": "2030-01-01",
	})
	planID := int64(plan["id"].(float64))

	svc, adminID := newContractTestService(t, env)
	guard := newBlockingVoidGuard()
	svc.SetVoidContractGuard(guard)
	ctx := platformCtx(adminID)

	voidDone := make(chan error, 1)
	go func() {
		_, err := svc.ChangeContractStatus(ctx, &dto.ContractStatusReq{
			ID: contractID, Status: crmmodel.ContractStatusVoid,
		})
		voidDone <- err
	}()

	// 等作废请求进入守卫（新实现已持有合同行锁；旧实现在事务外，无锁）。
	select {
	case <-guard.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("等待作废守卫进入超时")
	}

	amount, err := money.ParseMoney("10.00")
	if err != nil {
		t.Fatalf("解析金额: %v", err)
	}
	recordDone := make(chan error, 1)
	go func() {
		_, err := svc.CreateReceivableRecord(ctx, &dto.ReceivableRecordCreateReq{
			PlanID: planID, Amount: amount, ReceiptDate: time.Now().Format("2006-01-02"), Method: "bank",
		})
		recordDone <- err
	}()

	// 给登记回款足够时间抵达合同行锁（新实现阻塞在此；旧实现会直接完成）。
	time.Sleep(200 * time.Millisecond)
	close(guard.release)

	if err := waitResult(t, "作废合同", voidDone); err != nil {
		t.Fatalf("作废合同失败: %v", err)
	}
	if err := waitResult(t, "登记回款", recordDone); err == nil {
		t.Fatal("作废提交后登记回款应被拒绝（合同已作废），实际成功")
	}

	// 最终不变量：合同已作废，且该计划下没有新增回款记录。
	st, api := getContractRaw(t, env, admin, contractID)
	if st != http.StatusOK || api.Code != 0 {
		t.Fatalf("回读合同：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析合同详情: %v", err)
	}
	if detail["status"] != float64(crmmodel.ContractStatusVoid) {
		t.Fatalf("合同状态 = %v，期望已作废（4）", detail["status"])
	}
	if _, total := pageReceivableRecords(t, env, admin, fmt.Sprintf("&planId=%d", planID)); total != 0 {
		t.Fatalf("已作废合同下不应存在回款记录，实际 %d 条", total)
	}
}

// TestIntegrationContractEditApprovalSerialized P1：审批通过时持有合同行锁，
// 并发编辑在锁上等待；审批提交后编辑读到已签状态，金额保持审批时的值，
// 不会被基于「待审」旧状态的请求写回（元数据仍可编辑）。
func TestIntegrationContractEditApprovalSerialized(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "P1 编辑审批并发客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "P1 编辑审批并发合同", map[string]any{
		"customerId": customerID, "amount": "100.00",
	})
	contractID := int64(contract["id"].(float64))
	if st, api := changeContractStatus(t, env, admin, contractID, 2, ""); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}

	svc, adminID := newContractTestService(t, env)
	approval := newBlockingApproval()
	svc.SetApprovalService(approval)
	ctx := platformCtx(adminID)

	approveDone := make(chan error, 1)
	go func() {
		_, err := svc.ChangeContractStatus(ctx, &dto.ContractStatusReq{
			ID: contractID, Status: crmmodel.ContractStatusSigned,
		})
		approveDone <- err
	}()
	select {
	case <-approval.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("等待审批进入超时")
	}

	// 并发编辑：把金额改成 999（新实现会阻塞在合同行锁上，旧实现会读到待审后直接写回）。
	editedAmount, err := money.ParseMoney("999.00")
	if err != nil {
		t.Fatalf("解析金额: %v", err)
	}
	editDone := make(chan error, 1)
	go func() {
		editDone <- svc.UpdateContract(ctx, &dto.ContractUpdateReq{
			ID: contractID, Name: "P1 并发改名", Amount: editedAmount,
		})
	}()

	time.Sleep(200 * time.Millisecond)
	close(approval.release)

	if err := waitResult(t, "审批通过", approveDone); err != nil {
		t.Fatalf("审批通过失败: %v", err)
	}
	if err := waitResult(t, "并发编辑", editDone); err != nil {
		t.Fatalf("并发编辑失败: %v", err)
	}

	// 审批时的金额 100 必须保住：编辑请求在审批后读到已签状态，amount 被锁定。
	st, api := getContractRaw(t, env, admin, contractID)
	if st != http.StatusOK || api.Code != 0 {
		t.Fatalf("回读合同：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析合同详情: %v", err)
	}
	if detail["status"] != float64(crmmodel.ContractStatusSigned) {
		t.Fatalf("合同状态 = %v，期望已签（3）", detail["status"])
	}
	if detail["amount"] != "100.00" {
		t.Fatalf("已签合同金额 = %v，期望审批时的 100.00（并发编辑不得按旧状态写回）", detail["amount"])
	}
	if detail["name"] != "P1 并发改名" {
		t.Fatalf("已签合同元数据应允许编辑，名称 = %v", detail["name"])
	}

	// 审批后直接编辑金额同样被锁定（返回成功但金额不变）。
	if err := svc.UpdateContract(ctx, &dto.ContractUpdateReq{
		ID: contractID, Name: "P1 审批后再改", Amount: mustMoneyValue(t, "888.00"),
	}); err != nil {
		t.Fatalf("审批后编辑元数据: %v", err)
	}
	st, api = getContractRaw(t, env, admin, contractID)
	if st != http.StatusOK || api.Code != 0 {
		t.Fatalf("再次回读合同：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析合同详情: %v", err)
	}
	if detail["amount"] != "100.00" {
		t.Fatalf("审批后再改金额后仍应为 100.00，实际 %v", detail["amount"])
	}
}

// mustMoneyValue 解析金额字符串（供服务层 DTO 使用）。
func mustMoneyValue(t *testing.T, text string) money.Money {
	t.Helper()
	m, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", text, err)
	}
	return m
}
