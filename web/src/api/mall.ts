import request from '@/utils/request'

// mall 会员域 API（issue #174/#175，docs/mall-mvp-spec.md §4/§5）：管理端
// 会员/等级/地址/积分。商品/交易域与 C 端端点随 T3/T5 追加。
// 积分余额只经后端唯一入账入口变动（ApplyPointChange）：前端只有「调整」
// 触发端（adjustMemberPoint），不存在任何直改余额的接口。

// ---------- 会员（mall:member:*） ----------
// mobile 租户内唯一（后端撞键 409）；password 选填（空 = 不设/不改密码，
// 服务端 bcrypt 散列）；会员无删除端点（停启用覆盖生命周期，spec §4）。
export function pageMembers(params: any) {
  return request.get('/mall/member/page', { params }) as Promise<any>
}
export function getMember(id: number) {
  return request.get(`/mall/member/${id}`) as Promise<any>
}
export function createMember(data: {
  mobile: string
  password?: string
  nickname?: string
  avatarUrl?: string
  levelId?: number
  status?: number
  remark?: string
}) {
  return request.post('/mall/member', data) as Promise<any>
}
export function updateMember(data: {
  id: number
  mobile: string
  password?: string
  nickname?: string
  avatarUrl?: string
  levelId?: number
  status?: number
  remark?: string
}) {
  return request.put('/mall/member', data) as Promise<any>
}
// 停启用（PUT /mall/member/:id/status）：status 0 启用 / 1 停用。
export function updateMemberStatus(id: number, status: number) {
  return request.put(`/mall/member/${id}/status`, { status }) as Promise<any>
}

// ---------- 积分（issue #175） ----------
// 调整（POST /mall/member/adjust-point，挂 mall:member:update）：point 可正
// 可负，0 拒绝（400）；余额扣成负数 409；返回流水（含 balanceAfter 快照）。
export function adjustMemberPoint(data: { userId: number; point: number; remark?: string }) {
  return request.post('/mall/member/adjust-point', data) as Promise<any>
}
// 按会员流水（GET /mall/member/:id/point-records，挂 mall:member:query）：
// 分页，bizType 过滤选填（order_pay/order_cancel/admin_adjust）；id 正序
// （重放序：逐笔 point 累计恒等于 point_balance）。
export function pagePointRecords(userId: number, params: any) {
  return request.get(`/mall/member/${userId}/point-records`, { params }) as Promise<any>
}
// 积分配置（GET /mall/point-config，挂 mall:point:query）：租户级单行，
// 行缺失后端按默认 pointPerYuan=1 惰性建行。
export function getPointConfig() {
  return request.get('/mall/point-config') as Promise<any>
}
// 更新积分配置（PUT /mall/point-config，挂 mall:point:update）：
// pointPerYuan ≥ 0（0 = 支付不入账）。
export function updatePointConfig(data: { pointPerYuan: number; remark?: string }) {
  return request.put('/mall/point-config', data) as Promise<any>
}

// ---------- 会员等级（mall:level:*） ----------
// name/level 租户内唯一（后端撞键 409）；被会员引用禁删（后端 409）。
export function pageLevels(params: any) {
  return request.get('/mall/level/page', { params }) as Promise<any>
}
// 启用等级清单（登录 + mall:member:query）：会员表单等级下拉数据源。
export function listLevelOptions() {
  return request.get('/mall/level/list') as Promise<any>
}
export function getLevel(id: number) {
  return request.get(`/mall/level/${id}`) as Promise<any>
}
export function createLevel(data: { name: string; level: number; experience?: number; status?: number; remark?: string }) {
  return request.post('/mall/level', data) as Promise<any>
}
export function updateLevel(data: { id: number; name: string; level: number; experience?: number; status?: number; remark?: string }) {
  return request.put('/mall/level', data) as Promise<any>
}
export function deleteLevel(id: number) {
  return request.delete(`/mall/level/${id}`) as Promise<any>
}

// ---------- 收货地址（随 mall:member:query，只读） ----------
// 地址维护随 C 端自助（T2/T5），管理端只读：按会员查 + 管理视图分页。
export function pageAddresses(params: any) {
  return request.get('/mall/address/page', { params }) as Promise<any>
}
// 按会员查地址（不分页，会员详情抽屉数据源；member 详情已含 addresses，
// 单独刷新地址 tab 时使用）。
export function listMemberAddresses(userId: number) {
  return request.get(`/mall/address/member/${userId}`) as Promise<any>
}

// ---------- 商品分类（mall:category:*，issue #176） ----------
// 两级树：parentId 0=根，父必须是根级（后端 400）；同级 name 唯一（后端
// 409）；有子级或被 SPU 引用禁删（后端 409——SPU 引用计数随 T4 接线）。
export function treeProductCategories() {
  return request.get('/mall/product/category/tree') as Promise<any>
}
export function getProductCategory(id: number) {
  return request.get(`/mall/product/category/${id}`) as Promise<any>
}
export function createProductCategory(data: { parentId?: number; name: string; sort?: number; status?: number }) {
  return request.post('/mall/product/category', data) as Promise<any>
}
export function updateProductCategory(data: { id: number; parentId?: number; name: string; sort?: number; status?: number }) {
  return request.put('/mall/product/category', data) as Promise<any>
}
export function deleteProductCategory(id: number) {
  return request.delete(`/mall/product/category/${id}`) as Promise<any>
}

// ---------- 商品品牌（mall:brand:*，issue #176） ----------
// name 租户内唯一（后端 409）。
export function pageBrands(params: any) {
  return request.get('/mall/product/brand/page', { params }) as Promise<any>
}
export function getBrand(id: number) {
  return request.get(`/mall/product/brand/${id}`) as Promise<any>
}
export function createBrand(data: { name: string; picUrl?: string; status?: number; remark?: string }) {
  return request.post('/mall/product/brand', data) as Promise<any>
}
export function updateBrand(data: { id: number; name: string; picUrl?: string; status?: number; remark?: string }) {
  return request.put('/mall/product/brand', data) as Promise<any>
}
export function deleteBrand(id: number) {
  return request.delete(`/mall/product/brand/${id}`) as Promise<any>
}

// ---------- 商品属性（mall:property:*，issue #176） ----------
// 属性名 name 租户内唯一（后端 409）；属性值 (propertyId, name) 唯一（后端
// 409）；删除属性名后端级联删其属性值。
export function pageProperties(params: any) {
  return request.get('/mall/product/property/page', { params }) as Promise<any>
}
export function getProperty(id: number) {
  return request.get(`/mall/product/property/${id}`) as Promise<any>
}
export function createProperty(data: { name: string }) {
  return request.post('/mall/product/property', data) as Promise<any>
}
export function updateProperty(data: { id: number; name: string }) {
  return request.put('/mall/product/property', data) as Promise<any>
}
export function deleteProperty(id: number) {
  return request.delete(`/mall/product/property/${id}`) as Promise<any>
}
// 属性值：propertyId 归属属性名（body）；修改只可改名（归属不可改）。
export function listPropertyValues(propertyId: number) {
  return request.get(`/mall/product/property/${propertyId}/value`) as Promise<any>
}
export function createPropertyValue(data: { propertyId: number; name: string }) {
  return request.post('/mall/product/property/value', data) as Promise<any>
}
export function updatePropertyValue(data: { id: number; name: string }) {
  return request.put('/mall/product/property/value', data) as Promise<any>
}
export function deletePropertyValue(id: number) {
  return request.delete(`/mall/product/property/value/${id}`) as Promise<any>
}

// ---------- 商品 SPU/SKU（mall:spu:*，issue #177） ----------
// name 租户内唯一（后端 409）；分类必须叶子（后端 400）；SKU 规格引用的
// 属性值必须存在且归属对应属性（后端 400）；金额一律十进制字符串（Money
// 口径，不经 float）；SKU 规格名称快照由后端按主数据反查写定（前端只传
// propertyId/valueId）。删除约束：被订单引用禁删（后端 409，T6 生效）。
export function pageProductSPUs(params: any) {
  return request.get('/mall/product/spu/page', { params }) as Promise<any>
}
// 详情含 skus 数组（主 SKU 恒首位，properties 为规格快照 JSON）。
export function getProductSPU(id: number) {
  return request.get(`/mall/product/spu/${id}`) as Promise<any>
}
export function createProductSPU(data: {
  categoryId: number
  brandId?: number
  name: string
  picUrl?: string
  sliderPicUrls?: string[]
  detail?: string
  specType: number
  marketPrice?: string
  status?: number
  sort?: number
  skus: {
    properties?: { propertyId: number; valueId: number }[]
    price: string
    marketPrice?: string
    picUrl?: string
    stock: number
    first?: boolean
  }[]
}) {
  return request.post('/mall/product/spu', data) as Promise<any>
}
// 更新为字段全量覆盖 + SKU 集全量替换（旧 SKU 后端软删）。
export function updateProductSPU(data: {
  id: number
  categoryId: number
  brandId?: number
  name: string
  picUrl?: string
  sliderPicUrls?: string[]
  detail?: string
  specType: number
  marketPrice?: string
  status?: number
  sort?: number
  skus: {
    properties?: { propertyId: number; valueId: number }[]
    price: string
    marketPrice?: string
    picUrl?: string
    stock: number
    first?: boolean
  }[]
}) {
  return request.put('/mall/product/spu', data) as Promise<any>
}
// 上下架（PUT /mall/product/spu/:id/status）：status 0 下架 / 1 上架
//（只有上架可被购买，T5 生效）。
export function updateProductSPUStatus(id: number, status: number) {
  return request.put(`/mall/product/spu/${id}/status`, { status }) as Promise<any>
}
export function deleteProductSPU(id: number) {
  return request.delete(`/mall/product/spu/${id}`) as Promise<any>
}

// ---------- 交易订单（mall:trade:*，issue #180） ----------
// 管理端订单面：分页（状态/编号模糊/会员 ID/时间过滤）/详情（含订单项成交
// 快照与 payOrderNo 等管理字段全量）/管理员备注（任意状态可补注）/取消
//（仅待支付，后端其余状态 409，库存回补同事务）/完成（仅已支付 10→20，
// 后端走状态机）。金额一律十进制字符串（Money 口径）。
export function pageTradeOrders(params: any) {
  return request.get('/mall/trade/order/page', { params }) as Promise<any>
}
export function getTradeOrder(id: number) {
  return request.get(`/mall/trade/order/${id}`) as Promise<any>
}
// 管理员备注（PUT /mall/trade/order/:id/remark，挂 mall:trade:update）：
// ≤500 字，终态也允许补注。
export function updateTradeOrderRemark(id: number, remark: string) {
  return request.put(`/mall/trade/order/${id}/remark`, { remark }) as Promise<any>
}
// 取消（POST /mall/trade/order/:id/cancel，挂 mall:trade:cancel）：仅待支付；
// cancel_reason 由后端记「管理员取消」。
export function cancelTradeOrder(id: number) {
  return request.post(`/mall/trade/order/${id}/cancel`) as Promise<any>
}
// 完成（POST /mall/trade/order/:id/complete，挂 mall:trade:complete）：仅已支付。
export function completeTradeOrder(id: number) {
  return request.post(`/mall/trade/order/${id}/complete`) as Promise<any>
}
