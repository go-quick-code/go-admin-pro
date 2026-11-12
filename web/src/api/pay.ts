import request from '@/utils/request'

// ---------- 支付应用（租户级） ----------
export function pageApps(params: any) {
  return request.get('/pay/app/page', { params }) as Promise<any>
}
export function getApp(id: number) {
  return request.get(`/pay/app/${id}`) as Promise<any>
}
export function createApp(data: any) {
  return request.post('/pay/app', data) as Promise<any>
}
export function updateApp(data: any) {
  return request.put('/pay/app', data) as Promise<any>
}
export function deleteApp(id: number) {
  return request.delete(`/pay/app/${id}`) as Promise<any>
}
// 渠道绑定（全量替换，spec §5）：channelCodes 为绑定码数组。
export function getAppChannels(id: number) {
  return request.get(`/pay/app/${id}/channels`) as Promise<any>
}
export function updateAppChannels(id: number, channelCodes: string[]) {
  return request.put(`/pay/app/${id}/channels`, { channelCodes }) as Promise<any>
}

// ---------- 钱包（租户级，issue #132） ----------
// 只读：余额变动只走后端唯一入账入口（spec §4.3），本组无新增/编辑/删除接口。
export function pageWallets(params: any) {
  return request.get('/pay/wallet/page', { params }) as Promise<any>
}
// 自助端点（登录即可）：本人钱包（懒创建）与本人流水（服务层强制归属过滤）。
// 收银台/示例订单等后续票的前端页面经这两个接口取本人钱包数据。
export function getMyWallet() {
  return request.get('/pay/wallet/my') as Promise<any>
}
export function pageWalletTransactions(params: any) {
  return request.get('/pay/wallet-transaction/page', { params }) as Promise<any>
}
export function listMyWalletTransactions(params: any) {
  return request.get('/pay/wallet-transaction/my', { params }) as Promise<any>
}

// ---------- 支付订单与收银台（issue #133） ----------
// 交易单据只读（append-only，spec §4.10）：无新增/编辑/删除接口；下单由
// 消费方业务服务端调用（金额服务端定，spec §4.2），浏览器侧无金额入口。
export function createPayOrder(data: any) {
  return request.post('/pay/order/create', data) as Promise<any>
}
export function getCashierOrder(params: { appKey: string; merchantOrderId: string }) {
  return request.get('/pay/cashier/order', { params }) as Promise<any>
}
export function submitPayOrder(data: { orderId: number; channelCode: string }) {
  return request.post('/pay/order/submit', data) as Promise<any>
}
// mock 模拟回调（登录 + 本人订单 + 渠道为 mock）：收银台「模拟回调」按钮。
export function mockPayNotify(data: { orderId: number; result: 'success' | 'failure' }) {
  return request.post('/pay/mock/notify', data) as Promise<any>
}
export function pagePayOrders(params: any) {
  return request.get('/pay/order/page', { params }) as Promise<any>
}
export function getPayOrder(id: number) {
  return request.get(`/pay/order/${id}`) as Promise<any>
}

// ---------- 退款单（issue #135） ----------
// 交易单据只读（append-only，spec §4.10）：无新增/编辑/删除接口；退款发起由
// 消费方业务服务端调用（原路退回，累计 ≤ 实付服务端裁决 409，spec §4.4），
// 浏览器侧无发起入口。
export function createPayRefund(data: any) {
  return request.post('/pay/refund/create', data) as Promise<any>
}
export function pagePayRefunds(params: any) {
  return request.get('/pay/refund/page', { params }) as Promise<any>
}
export function getPayRefund(id: number) {
  return request.get(`/pay/refund/${id}`) as Promise<any>
}

// ---------- 回调通知（issue #134） ----------
// 交易单据只读（append-only，spec §4.10）：任务列表 + 执行日志子表，无写入接口。
export function pageNotifyTasks(params: any) {
  return request.get('/pay/notify-task/page', { params }) as Promise<any>
}
// 详情含执行日志（logs，append-only 时间线）。
export function getNotifyTask(id: number) {
  return request.get(`/pay/notify-task/${id}`) as Promise<any>
}

// ---------- 支付渠道（平台级，仅平台租户可管理） ----------
export function pageChannels(params: any) {
  return request.get('/pay/channel/page', { params }) as Promise<any>
}
// 渠道只启停 + 凭证合并（提交 ****** 保留原值），14 行种子常驻禁删（spec §4.10）。
export function updateChannel(id: number, data: { status?: number; config?: string; remark?: string }) {
  return request.put(`/pay/channel/${id}`, data) as Promise<any>
}
// 租户侧只读清单（code/名称/状态，无凭证）：应用详情绑定 tab 数据源。
export function listTenantChannels() {
  return request.get('/pay/channel/list') as Promise<any>
}

// ---------- 充值套餐（issue #136） ----------
// 管理端 CRUD（pay:wallet-recharge-package:*）；金额一律十进制字符串
//（后端 money.Money 只接受 JSON 字符串字面量，精度不经 float）。
export function pageRechargePackages(params: any) {
  return request.get('/pay/wallet-recharge-package/page', { params }) as Promise<any>
}
export function getRechargePackage(id: number) {
  return request.get(`/pay/wallet-recharge-package/${id}`) as Promise<any>
}
export function createRechargePackage(data: { name: string; payPrice: string; bonusPrice?: string; status?: number }) {
  return request.post('/pay/wallet-recharge-package', data) as Promise<any>
}
export function updateRechargePackage(data: { id: number; name: string; payPrice: string; bonusPrice?: string; status?: number }) {
  return request.put('/pay/wallet-recharge-package', data) as Promise<any>
}
// 被充值单引用时后端 409（spec §4.10 引用禁删）。
export function deleteRechargePackage(id: number) {
  return request.delete(`/pay/wallet-recharge-package/${id}`) as Promise<any>
}
// 自助清单（登录即可）：我的充值页「发起充值」的套餐选择数据源（仅启用档位）。
export function listRechargePackageOptions() {
  return request.get('/pay/wallet-recharge-package/list') as Promise<any>
}

// ---------- 充值单（issue #136） ----------
// 发起充值（登录 + 本人钱包）：packageId（套餐档位快照）与 payPrice（自定义，
// bonus 恒 0）二选一；同事务挂支付订单，返回 appKey/merchantOrderId 供收银台
// 跳转（钱包渠道被禁，spec §4.7）。
export function createRecharge(data: { packageId?: number; payPrice?: string }) {
  return request.post('/pay/wallet-recharge/create', data) as Promise<any>
}
// 我的充值（登录即可，服务层强制本人钱包归属，spec §4.11）。
export function pageMyRecharges(params: any) {
  return request.get('/pay/wallet-recharge/my', { params }) as Promise<any>
}
// 充值单管理分页（pay:wallet-recharge:query）：只读（资金事实 append-only）。
export function pageRecharges(params: any) {
  return request.get('/pay/wallet-recharge/page', { params }) as Promise<any>
}

// ---------- 转账单（issue #137） ----------
// 发起转账（消费方服务端端点，登录；同 #133 下单契约——幂等/渠道校验后端
// 收口，wallet 渠道 userAccount 为目标用户标识）。管理页无发起入口（示例提现
// 审批是唯一演示发起方）。
export function createPayTransfer(data: any) {
  return request.post('/pay/transfer/create', data) as Promise<any>
}
// 转账单管理分页/详情（pay:transfer:query）：只读（资金事实 append-only，
// spec §4.10；导出随 #138 export-excel 补种）。
export function pagePayTransfers(params: any) {
  return request.get('/pay/transfer/page', { params }) as Promise<any>
}
export function getPayTransfer(id: number) {
  return request.get(`/pay/transfer/${id}`) as Promise<any>
}

// ---------- 示例订单（issue #137） ----------
// 演示商品字典（登录即可）：示例下单表单的商品选择数据源（名称/金额服务端
// 字典定，请求只收 spuId，spec §3.5/§4.2）。
export function listDemoSPUs() {
  return request.get('/pay/demo-order/spus') as Promise<any>
}
// 创建示例订单（登录）：返回挂单定位（appKey + merchantOrderId）供跳收银台。
export function createDemoOrder(data: { spuId: number }) {
  return request.post('/pay/demo-order/create', data) as Promise<any>
}
// 示例订单分页（pay:demo-order:query）。
export function pageDemoOrders(params: any) {
  return request.get('/pay/demo-order/page', { params }) as Promise<any>
}
// 发起退款（登录 + 本人订单）：全额退未退余额，成功经 #134 回调链路联动。
export function refundDemoOrder(id: number, reason?: string) {
  return request.put(`/pay/demo-order/${id}/refund`, { reason }) as Promise<any>
}

// ---------- 示例提现（issue #137） ----------
// 申请提现（登录）：user_account/user_name 服务端取登录主体。
export function createDemoWithdraw(data: { subject: string; price: string }) {
  return request.post('/pay/demo-withdraw/create', data) as Promise<any>
}
// 示例提现分页（pay:demo-withdraw:query）。
export function pageDemoWithdraws(params: any) {
  return request.get('/pay/demo-withdraw/page', { params }) as Promise<any>
}
// 审批（pay:demo-withdraw:approve）：创建转账单（wallet 渠道入账申请人钱包），
// 转账成功经 #134 回调链路联动提现单 10。
export function approveDemoWithdraw(id: number) {
  return request.put(`/pay/demo-withdraw/${id}/approve`) as Promise<any>
}
// 关闭（pay:demo-withdraw:close）：仅等待状态且未发起转账可关，置 20。
export function closeDemoWithdraw(id: number) {
  return request.put(`/pay/demo-withdraw/${id}/close`) as Promise<any>
}
