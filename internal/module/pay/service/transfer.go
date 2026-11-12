package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// transfer.go 是转账域（issue #137，docs/pay-mvp-spec.md §3.2/§4.8/§4.9/§4.10/
// §8.2 转账行）。
//
// 发起 POST /pay/transfer/create（消费方服务端端点，登录即可，同 #133 下单
// 契约）：merchant_transfer_id 幂等（撞 UNIQUE 返回原单不报错，跨应用/跨租户
// 同号不冲突）；渠道按请求选择（wallet/mock）——校验「码 ∈ 14 常量 ∧ 注册表
// 已实现 ∧ 平台渠道启用」各 409；**不校验应用渠道绑定**（决策：spec §4.8 未
// 定义转账的绑定约束，绑定守卫是收银台 submit 的支付语义；示例提现审批直接
// 以配置应用发起转账，绑定与否不影响资金事实）；ZZ 单号 pay_sequence 事务内
// 发放（DTO 不收 no）；notify_url 缺省取应用 transfer_notify_url 快照；
// user_ip 服务端取客户端 IP。
//
// 渠道 Transfer（发起事务内，spec §4.8）：
//   - wallet：user_account = 目标用户标识（正整数 user_id 字符串，非法 409），
//     同事务经唯一入账入口入账目标钱包（biz=6，正价，biz_id=转账单 ID；目标
//     钱包懒创建，spec §4.3），回执取目标钱包流水 LS 单号；user_id/user_type
//     随之落转账单（本系统内账户有归属）；
//   - mock：模拟成功（spec §4.8 仅「模拟成功」，无失败分支——转账失败即发起
//     异常，保持最小实现）；user_id/user_type 留 0（外部账户无归属，spec §3.2）。
//
// 成功统一走转账成功入账函数（transferSuccessTx，两渠道共用）：转账单认领
// 0→10（0 行 = 已处理，幂等返回）→ success_time → 建商户回调任务（type=3，
// max_notify_times=9）。函数内不做任何余额变动（wallet 入账在渠道 Transfer
// 内，先入账后认领，认领失败整体回滚）。
//
// 越权防线（spec §4.11）：发起端点只挂登录（消费方服务端契约）；管理端点挂
// pay:transfer:query（管理页不设 owner 行级数据权限，租户内全量可见）；
// 跨租户业务 ID 一律 404。转账为平台出账，v1 无平台资金账本（差异清单）。

// ---------- 应用删除引用检查（spec §4.10，#137 注册转账引用） ----------

// TransferRefChecker 是转账单引用检查（AppRefChecker 实现）：应用存在转账单
// 禁删。与 OrderRefChecker/RefundRefChecker 以 RefCheckerChain 组合注册
// （internal/app 装配处）。示例订单/示例提现不另设检查器（决策见
// DemoRefCheckerDecision）。
type TransferRefChecker struct {
	Repo *payrepo.Repository
}

// AppHasReference 实现 AppRefChecker：有转账单（任意状态）即引用。
func (c TransferRefChecker) AppHasReference(ctx context.Context, appID int64) (bool, string, error) {
	total, err := c.Repo.CountTransfersByAppID(ctx, appID)
	if err != nil {
		return false, "", err
	}
	return total > 0, "转账单", nil
}

// DemoRefCheckerDecision 记录示例业务不设 AppRefChecker 的决策（issue #137）：
// 示例订单创建时同事务挂支付订单（pay_order.app_id 指向应用），示例提现审批
// 创建转账单（pay_transfer.app_id 同理）——两表对应用的引用全部经由其关联的
// pay 交易单据传递，OrderRefChecker/TransferRefChecker 已先行命中（消费行存在
// ⟹ 关联交易单据存在，同事务原子），再加 DemoOrderRefChecker 属重复报告。
// 示例行自身引用的是 pay 单据 ID（pay_order_id/pay_transfer_id），不是应用 ID。

// ---------- 发起转账（POST /pay/transfer/create，登录） ----------

// CreateTransfer 创建转账单并按请求渠道发起转账：merchant_transfer_id 幂等
// ——同租户同应用撞幂等键返回原单（200 不报错），跨应用/跨租户同号不冲突。
// 见包内 transfer.go 头注释的校验矩阵与两分支语义。
func (s *Service) CreateTransfer(ctx context.Context, req *dto.TransferCreateReq, userIP string) (*model.Transfer, error) {
	p, ok := authn.From(ctx)
	if !ok || p.UserID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return nil, xerr.Unauthorized("")
	}
	appKey := trimString(req.AppKey)
	merchantTransferID := trimString(req.MerchantTransferID)
	subject := trimString(req.Subject)
	if appKey == "" || merchantTransferID == "" || subject == "" {
		return nil, xerr.BadRequest("应用标识、消费方转账单号与转账标题不能为空")
	}
	if len(merchantTransferID) > 64 {
		return nil, xerr.BadRequest("消费方转账单号不能超过 64 字")
	}
	if req.Price.Cents() <= 0 {
		return nil, xerr.BadRequest(fmt.Sprintf("转账金额必须大于 0：%s", req.Price))
	}
	channelCode := trimString(req.ChannelCode)

	// 幂等快路径：已有同号转账单直接返回原单（不校验金额一致性——幂等语义
	// 消费方自负其责，与 #133 下单口径一致）。
	app, err := s.findAppByKey(ctx, appKey)
	if err != nil {
		return nil, err
	}
	if existing, err := s.repo.FindTransferByMerchantID(ctx, app.ID, merchantTransferID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	var out *model.Transfer
	err = s.repo.Transaction(ctx, func(tx *payrepo.Repository) error {
		out, err = s.createTransferTx(ctx, tx, tenantID, app, transferParams{
			MerchantTransferID: merchantTransferID,
			ChannelCode:        channelCode,
			Subject:            subject,
			Price:              req.Price,
			UserAccount:        trimString(req.UserAccount),
			UserName:           trimString(req.UserName),
			NotifyURL:          trimString(req.NotifyURL),
			UserIP:             userIP,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	// 回读终态（同步成功 10）。
	return s.repo.FindTransferByID(ctx, out.ID)
}

// transferParams 是 createTransferTx 的一次转账发起参数（HTTP 端点与示例提现
// 审批共用；金额/标题一律发起方快照，渠道请求不含任何客户端输入金额）。
type transferParams struct {
	MerchantTransferID string
	ChannelCode        string
	Subject            string
	Price              money.Money
	UserAccount        string
	UserName           string
	NotifyURL          string
	UserIP             string
}

// createTransferTx 是转账发起的事务内核（HTTP 端点开事务调用；示例提现审批在
// 其审批事务内复用——钱包入账与转账单落库/认领/建任务原子）：ZZ 单号发放 →
// 转账单落库（撞幂等键回读原单返回）→ 渠道 Transfer（wallet 同事务入账）→
// transferSuccessTx 认领建任务。
func (s *Service) createTransferTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, app *model.App, params transferParams) (*model.Transfer, error) {
	// 渠道校验（码 ∈ 14 常量 / 注册表已实现 / 平台启用）各 409 文案可区分；
	// 不校验应用绑定（决策见 transfer.go 头注释）。
	if !channel.IsKnownCode(params.ChannelCode) {
		return nil, xerr.Conflict("未知支付渠道码：" + params.ChannelCode)
	}
	client := s.channelRegistry().Get(params.ChannelCode)
	if client == nil {
		return nil, xerr.Conflict("渠道未实现：" + channel.Name(params.ChannelCode) + "（" + params.ChannelCode + "）")
	}
	rows, err := tx.ListChannelsByCodes(ctx, []string{params.ChannelCode})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0].Status != model.StatusEnable {
		return nil, xerr.Conflict("支付渠道已停用：" + channel.Name(params.ChannelCode) + "（" + params.ChannelCode + "）")
	}

	// ZZ 单号：pay_sequence 事务内发放（spec §4.9），DTO 不收 no（客户端提交
	// 的 no 一律不采纳）。
	no, err := s.nextNoInTx(ctx, tx, tenantID, SeqTransfer, NoPrefixTransfer)
	if err != nil {
		return nil, err
	}
	notifyURL := params.NotifyURL
	if notifyURL == "" {
		notifyURL = app.TransferNotifyURL
	}
	// 归属解析（wallet 渠道要求合法目标，spec §4.8）：user_account 解析出的
	// 目标用户随之落转账单 user_id/user_type（本系统内账户有归属）；mock 渠道
	// 不要求目标（外部账户无归属，user_id/user_type 留 0，spec §3.2）。解析在
	// 事务内核统一做（HTTP 端点与示例提现审批共用同一口径）。
	var targetUserID int64
	if params.ChannelCode == channel.CodeWallet {
		targetUserID, err = parseWalletTarget(params.UserAccount)
		if err != nil {
			return nil, err
		}
	}
	out := &model.Transfer{
		AppID:              app.ID,
		No:                 no,
		MerchantTransferID: params.MerchantTransferID,
		UserID:             targetUserID,
		UserType:           model.WalletUserTypeAdmin,
		ChannelCode:        params.ChannelCode,
		Subject:            params.Subject,
		Price:              params.Price,
		UserAccount:        params.UserAccount,
		UserName:           params.UserName,
		NotifyURL:          notifyURL,
		UserIP:             params.UserIP,
	}
	if targetUserID == 0 {
		// 外部账户无归属（mock 渠道）：user_type 留 0（spec §3.2）。
		out.UserType = 0
	}
	if err := tx.InsertTransfer(ctx, out); err != nil {
		if !payrepo.IsDuplicateEntry(err) {
			return nil, err
		}
		// 撞幂等键：并发首触的赢家已提交（INSERT 等待其定局），回读原单
		//（赢家同事务完成了入账/认领/建任务，原单即终态）。
		existing, ferr := tx.FindTransferByMerchantID(ctx, app.ID, params.MerchantTransferID)
		if ferr != nil {
			return nil, ferr
		}
		if existing == nil {
			return nil, xerr.Conflict("转账单创建冲突，请重试：" + params.MerchantTransferID)
		}
		return existing, nil
	}

	// 渠道统一动作：wallet 在 ctx 事务内入账目标钱包（biz=6），mock 模拟成功。
	// 金额一律转账单快照，渠道请求不含任何客户端输入金额。
	resp, err := client.Transfer(payrepo.WithTx(ctx, tx), channel.TransferReq{
		OutTransferNo: out.No,
		Subject:       out.Subject,
		Price:         out.Price,
		UserAccount:   out.UserAccount,
		UserName:      out.UserName,
		BizID:         out.ID,
	})
	if err != nil {
		// 入账失败（目标标识非法等）：整体回滚，转账单不落库、余额不变。
		return nil, err
	}
	switch resp.Status {
	case channel.TransferStatusSuccess:
		claimed, err := s.transferSuccessTx(ctx, tx, tenantID, out, resp.ChannelTransferNo, resp.Raw)
		if err != nil {
			return nil, err
		}
		if !claimed {
			// 同转账单并发已被认领：回滚本次入账（409，余额不变）。
			return nil, xerr.Conflict("转账单已处理，请勿重复发起")
		}
	case channel.TransferStatusFail:
		// v1 无渠道返回失败的路径（wallet 入账失败即 error 回滚、mock 恒成功）；
		// 分支与退款 UpdateRefundResult 同构，防御性保留。
		return nil, tx.UpdateTransferResult(ctx, tenantID, out.ID, model.TransferStatusClosed, "TRANSFER_FAIL", "渠道返回转账失败", resp.Raw)
	default:
		// 处理中（异步渠道预留）：转账单停 0 等待，等渠道回调后走
		// transferSuccessTx（channel-notify 端点随真实渠道实现承接）。
		return out, nil
	}
	return out, nil
}

// transferSuccessTx 转账成功入账（wallet 发起 / mock 模拟成功 / 未来真实渠道
// 转账回调共用；spec §4.8）：
//  1. 转账单状态认领 UPDATE ... WHERE id=? AND status=0——0 行 = 已被并发方
//     处理，幂等成功返回（不重复建任务）；
//  2. success_time + 渠道回执（channel_transfer_no/末次响应原文）；
//  3. 建商户回调任务（type=3，data_id=转账单 ID，merchant_transfer_id，
//     notify_url 发起快照，max_notify_times=9，next_notify_time=now；job
//     消费是 #134）。
//
// 函数内不做任何余额变动：wallet 入账发生在渠道 Transfer 内（先入账后认领，
// 认领失败整体回滚），mock/真实渠道资金为渠道侧出账。
func (s *Service) transferSuccessTx(ctx context.Context, tx *payrepo.Repository, tenantID int64, transfer *model.Transfer, channelTransferNo, notifyData string) (bool, error) {
	successTime := s.businessNow()
	claimRows, err := tx.ClaimTransferSuccess(ctx, tenantID, transfer.ID, channelTransferNo, notifyData, successTime)
	if err != nil {
		return false, err
	}
	if claimRows == 0 {
		return false, nil
	}
	task := &model.NotifyTask{
		AppID:              transfer.AppID,
		Type:               model.NotifyTypeTransfer,
		DataID:             transfer.ID,
		MerchantTransferID: transfer.MerchantTransferID,
		Status:             model.NotifyTaskStatusWaiting,
		NotifyURL:          transfer.NotifyURL,
		NextNotifyTime:     &successTime,
		NotifyTimes:        0,
		MaxNotifyTimes:     model.NotifyTaskMaxTimes,
	}
	return true, tx.InsertNotifyTask(ctx, task)
}

// ---------- wallet 渠道转账入账后端（channel.WalletTransferer 实现） ----------

// TransferByWallet 实现 channel.WalletTransferer（wallet 渠道入账后端）：从
// ctx 取发起事务，解析 req.UserAccount 为目标用户标识（非法 409——wallet 渠道
// 要求合法目标，spec §4.8），经唯一入账入口入账目标用户钱包（biz=6 转账，正价
// 入账，biz_id=转账单 ID，目标钱包懒创建，spec §4.3）。返回渠道转账回执（目标
// 钱包流水 LS 单号——资金在本租户账本内移动，流水即回执）。不做转账单状态变更
// （认领/建任务由发起方在入账成功后走转账成功入账函数）。
func (s *Service) TransferByWallet(ctx context.Context, req channel.TransferReq) (string, error) {
	tx, ok := payrepo.TxFrom(ctx)
	if !ok {
		return "", xerr.Internal(errors.New("wallet 渠道转账缺少事务上下文"))
	}
	tenantID := payTenantID(ctx)
	if tenantID == 0 {
		return "", xerr.Unauthorized("")
	}
	userID, err := parseWalletTarget(req.UserAccount)
	if err != nil {
		return "", err
	}
	out, err := s.applyWalletChangeTx(ctx, tx, tenantID, WalletChange{
		UserID:   userID,
		UserType: model.WalletUserTypeAdmin,
		BizType:  model.WalletBizTransfer,
		BizID:    req.BizID,
		Title:    clampTitle("转账：" + req.Subject),
		Price:    money.FromCents(req.Price.Cents()),
	})
	if err != nil {
		return "", err
	}
	return out.No, nil
}

// parseWalletTarget 解析 wallet 渠道收款账号为目标用户标识（纯函数校验）：
// 必须是正整数字符串（user_id）。非合法标识 409（spec §4.8 wallet 渠道要求
// 合法目标）。v1 不做跨模块用户存在性校验（pay 无 system 用户依赖，钱包按
// spec §4.3 对任意正整数 user_id 懒创建）——「未知目标 409」指无法解析出
// 合法标识的账号。
func parseWalletTarget(userAccount string) (int64, error) {
	userID, err := strconv.ParseInt(strings.TrimSpace(userAccount), 10, 64)
	if err != nil || userID <= 0 {
		return 0, xerr.Conflict(fmt.Sprintf("wallet 渠道收款账号必须是合法的用户标识（正整数）：%s", userAccount))
	}
	return userID, nil
}

// ---------- 商户回调报文详情（type=3 转账，#137 的 NotifyPayloadEnricher） ----------

// enrichTransferPayload 实现 type=3（转账）回调报文详情扩展（service.New 内
// 注册，与 #135 type=2 同形态，spec §4.5）：转账单号（复用报文 orderNo 字段
// ——通用单号位）/单据标题/渠道转账回执/金额/成功时间；商户以
// merchant_transfer_id（任务自带）关联自己的转账单。
func (s *Service) enrichTransferPayload(ctx context.Context, task *model.NotifyTask, payload *NotifyPayload) error {
	transfer, err := s.repo.FindTransferByID(ctx, task.DataID)
	if err != nil {
		return err
	}
	if transfer == nil {
		return fmt.Errorf("回调任务 %d 对应的转账单 %d 不存在", task.ID, task.DataID)
	}
	payload.OrderNo = transfer.No
	payload.Subject = transfer.Subject
	payload.ChannelOrderNo = transfer.ChannelTransferNo
	text, err := transfer.Price.MarshalText()
	if err != nil {
		return fmt.Errorf("编码转账金额: %w", err)
	}
	payload.Price = string(text)
	if transfer.SuccessTime != nil {
		payload.SuccessTime = transfer.SuccessTime.Format(time.RFC3339)
	}
	return nil
}

// ---------- 管理端（权限码 pay:transfer:query，spec §4.11/§7） ----------

// ListTransfers 分页查询转账单（管理端）：pay 管理页不设 owner 行级数据权限
// （记录无创建人语义，spec §4.11），租户内全量可见。
func (s *Service) ListTransfers(ctx context.Context, req *dto.TransferPageReq) ([]model.Transfer, int64, error) {
	if req.Status != nil {
		if err := validateTransferStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListTransfers(ctx, payrepo.TransferFilter{
		No:                 trimString(req.No),
		MerchantTransferID: trimString(req.MerchantTransferID),
		AppID:              req.AppID,
		ChannelCode:        trimString(req.ChannelCode),
		Status:             req.Status,
	}, page.Offset(), page.PageSize)
}

// GetTransfer 转账单详情（附关联支付应用，前端详情 drawer 展示应用定位）。
func (s *Service) GetTransfer(ctx context.Context, id int64) (*dto.TransferDetail, error) {
	transfer, err := s.repo.FindTransferByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if transfer == nil {
		return nil, xerr.NotFound("转账单不存在")
	}
	app, err := s.repo.FindAppByID(ctx, transfer.AppID)
	if err != nil {
		return nil, err
	}
	return &dto.TransferDetail{Transfer: transfer, App: app}, nil
}

// validateTransferStatus 校验转账单状态过滤值枚举。
func validateTransferStatus(status int8) error {
	switch status {
	case model.TransferStatusWaiting, model.TransferStatusDoing,
		model.TransferStatusSuccess, model.TransferStatusClosed:
		return nil
	default:
		return xerr.BadRequest(fmt.Sprintf("转账单状态不合法：%d（0 等待 / 5 进行中 / 10 成功 / 20 关闭）", status))
	}
}

// guardTenantContext 是转账/示例联动的租户上下文守卫（公开回调联动派生租户后
// 携带执行）：租户 ID 非法一律拒绝（不落 tenant 0 脏数据，同 ApplyWalletChange
// 口径）。
func guardTenantContext(ctx context.Context) (context.Context, int64, error) {
	tenantID := tenant.ID(ctx)
	if tenantID <= 0 {
		return ctx, 0, xerr.Internal(errors.New("回调联动缺少租户上下文"))
	}
	return ctx, tenantID, nil
}
