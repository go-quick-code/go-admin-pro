# OA MVP 规格与施工顺序

> 状态：待确认。里程碑是一个（bpm + 独立 OA + 站内通知），下面的编号只是施工顺序，不是对外切片。
> 术语以 `GLOSSARY.md` 为准；架构决策见 `docs/adr/0001`–`0007`，本里程碑新增决策见 `docs/adr/0008`（bpm 运行模型）与 `docs/adr/0009`（OA 域边界）。

## 1. 范围与施工顺序

1. **bpm 内核 + notify 基础 + CRM 合同审批迁移**：流程定义版本化、实例/任务/审批记录、候选池任务、通用待办；站内通知表与接口；合同审批从直通实现切到 bpm。
2. **公告**：草稿/发布/撤回、受众、已读回执、附件、富文本清洗。
3. **请假**：工作日历、请假类型/额度、半天粒度、bpm 审批、撤销返还、考勤联动接点。
4. **报销**：表头 + 明细行、金额合计、bpm 审批。
5. **日程 + 会议室**：可见性、月/周视图、会议室冲突校验、预约自动生成日程。
6. **文档**：目录树、目录 ACL 继承、文件上传/下载。
7. **考勤**：班次/考勤组/Web 打卡/结果派生/月度汇总/管理员校正/节假日与调休。

施工顺序按上表串行；每个阶段完成后再进入下一阶段。考勤放最后，因为它依赖请假与工作日历。

## 2. 模块边界与依赖

| 模块 | 职责 | 依赖 |
|---|---|---|
| `internal/module/bpm` | 流程定义、实例、任务、审批记录、通用待办、`BusinessHandler` 注册 | `system`（用户/部门/角色/权限解析）、`notify` |
| `internal/module/oa` | 公告、请假、报销、考勤、日程、会议室、文档 | `bpm`（请假/报销）、`system`、`infra`（文件/定时任务）、`notify` |
| `internal/module/notify` | 站内通知投递、未读数、已读状态 | `system`（仅用户/租户上下文） |
| `internal/module/crm` | 合同提交审批；不再内嵌审批实现 | `bpm`（通过 `ApprovalService` 适配器） |

- bpm 不反向依赖 CRM/OA；业务状态回写通过 `BusinessHandler` 接口，由 `internal/app` 装配时注册。
- notify 是平台级模块，不放在 `oa` 或 `system`；bpm/OA/CRM 通过 `notify.Service` 在各自事务内写通知。
- CRM 合同审批动作改在通用待办页完成；合同页只展示状态、审批时间线与当前待办入口。

## 3. 表结构

所有实体表含 `id`、`tenant_id`、`created_at`、`updated_at`、`deleted_at`、`creator`、`updater`。关联表用复合主键 + `tenant_id`，不设软删除（需要时单独说明）。审批记录、公告已读、通知是追加型表，不设 `creator/updater`，只保留业务时间。

### 3.1 bpm

| 表 | 关键字段 |
|---|---|
| `bpm_process_definition` | `key`、`version`、`name`、`business_type`、`nodes` JSON、`status`(0 草稿/1 发布/2 停用)、`published_at`、`remark`；唯一键 `(tenant_id, key, version)` |
| `bpm_process_instance` | `definition_id`、`definition_key`、`definition_version`、`business_type`、`business_id`、`initiator_id`、`title`、`summary`、`status`(0 运行中/1 通过/2 驳回/3 撤回)、`started_at`、`ended_at`；索引 `(tenant_id, business_type, business_id)`、`(tenant_id, initiator_id)` |
| `bpm_task` | `instance_id`、`node_index`、`node_name`、`assignee_id`、`status`(0 待办/1 通过/2 驳回/3 转办/4 取消)、`handled_at`；索引 `(tenant_id, assignee_id, status)`、`(instance_id, node_index, status)` |
| `bpm_approval_record` | `instance_id`、`task_id`、`node_index`、`node_name`、`operator_id`、`action`(1 通过/2 驳回/3 撤回/4 转办)、`comment`、`transfer_to_user_id`、`created_at`；只追加 |

流程定义的 `nodes` 是节点数组，每个节点包含：`name`、`assigneeType`、`assigneeValue`、`deptId`（仅指定部门负责人时使用）。`assigneeType` 取值：

| `assigneeType` | `assigneeValue` 含义 |
|---|---|
| `user` | 指定用户 ID；单节点单人 |
| `role` | 角色 ID；该角色所有启用的用户组成候选池 |
| `permission` | 权限码；持有该权限的启用用户组成候选池（`super_admin` 的 `*` 视为持有全部权限） |
| `initiator` | 发起人本人 |
| `initiator_dept_leader` | 发起人所在部门负责人；负责人空缺时沿部门树向上查找 |
| `dept_leader` | `deptId` 指定部门的负责人；负责人空缺时沿部门树向上查找 |

### 3.2 notify

| 表 | 关键字段 |
|---|---|
| `notify_message` | `user_id`、`type`(`bpm_todo`/`bpm_result`/`announcement`/`meeting_reminder`)、`title`、`content`、`biz_type`、`biz_id`、`route`、`read_at`、`created_at`；索引 `(tenant_id, user_id, read_at, id)`、`(tenant_id, user_id, id)` |

### 3.3 OA 公共

| 表 | 关键字段 |
|---|---|
| `oa_workday_calendar` | `date`、`type`(0 工作日/1 休息日/2 节假日)、`remark`；唯一键 `(tenant_id, date)` |

没有显式记录时，周一至周五默认工作日、周六周日默认休息日；`date` 有记录时以记录为准，`type=0` 可表达调休上班。

### 3.4 公告

| 表 | 关键字段 |
|---|---|
| `oa_announcement` | `title`、`content`(清洗后的 HTML)、`status`(0 草稿/1 发布/2 撤回)、`audience_type`(0 全员/1 自定义)、`pinned`、`published_at`、`withdrawn_at` |
| `oa_announcement_target` | `announcement_id`、`target_type`(1 部门/2 角色)、`target_id`；复合主键 |
| `oa_announcement_read` | `announcement_id`、`user_id`、`read_at`；唯一键 `(tenant_id, announcement_id, user_id)` |

附件使用 `infra_file`，`biz_type='oa_announcement'`、`biz_id=公告 ID`。

### 3.5 请假

| 表 | 关键字段 |
|---|---|
| `oa_leave_type` | `name`、`code`、`balance_managed`、`annual_quota` decimal(5,1)、`status`、`sort`、`remark` |
| `oa_leave_balance` | `user_id`、`leave_type_id`、`year`、`quota` decimal(5,1)、`adjustment` decimal(5,1)、`used` decimal(5,1)；唯一键 `(tenant_id, user_id, leave_type_id, year)` |
| `oa_leave_request` | `user_id`、`leave_type_id`、`start_date`、`start_half`(0 上午/1 下午)、`end_date`、`end_half`(0 上午/1 下午)、`duration_days` decimal(5,1)、`reason`、`status`(0 草稿/1 待审/2 通过/3 驳回/4 撤回/5 已取消)、`reject_reason`、`approval_instance_id` |

附件使用 `infra_file`，`biz_type='oa_leave'`、`biz_id=请假 ID`。

### 3.6 报销

| 表 | 关键字段 |
|---|---|
| `oa_expense_claim` | `user_id`、`title`、`total_amount` decimal(12,2)、`reason`、`status`(0 草稿/1 待审/2 通过/3 驳回/4 撤回)、`reject_reason`、`approval_instance_id` |
| `oa_expense_item` | `claim_id`、`category`、`amount` decimal(12,2)、`expense_date`、`remark`、`sort` |

费用类别使用系统字典 `oa_expense_category`；附件使用 `infra_file`，`biz_type='oa_expense'`、`biz_id=报销单 ID`。

### 3.7 日程与会议室

| 表 | 关键字段 |
|---|---|
| `oa_calendar_event` | `creator_id`、`title`、`description`、`start_time`、`end_time`、`all_day`、`visibility`(0 私有/1 部门/2 公开)、`dept_id`、`source`(0 手动/1 会议室预约)、`source_id`、`status`(0 有效/1 取消) |
| `oa_meeting_room` | `name`、`location`、`capacity`、`status`、`sort` |
| `oa_room_booking` | `room_id`、`user_id`、`title`、`start_time`、`end_time`、`status`(0 有效/1 取消)、`calendar_event_id`、`reminded_at` |

### 3.8 文档

| 表 | 关键字段 |
|---|---|
| `oa_document_folder` | `parent_id`、`name`、`owner_id`、`sort` |
| `oa_document_folder_acl` | `folder_id`、`target_type`(0 全员/1 部门/2 角色/3 用户)、`target_id`；复合主键 |
| `oa_document_file` | `folder_id`、`file_id`(infra_file ID)、`name`、`size`、`type`、`uploader_id` |

### 3.9 考勤

| 表 | 关键字段 |
|---|---|
| `oa_attendance_shift` | `name`、`start_time`、`end_time`、`grace_minutes`、`status`、`sort` |
| `oa_attendance_group` | `name`、`shift_id`、`status` |
| `oa_attendance_group_user` | `group_id`、`user_id`；唯一键 `(tenant_id, user_id)`（一个用户只属于一个组） |
| `oa_attendance_punch` | `user_id`、`punch_date`、`punch_time`、`type`(0 上班/1 下班)、`source`(0 Web)、`ip`、`user_agent` |
| `oa_attendance_result` | `user_id`、`work_date`、`shift_id`、`in_time`、`out_time`、`late_minutes`、`early_minutes`、`missing_in`、`missing_out`、`leave_days` decimal(3,1)、`status`、`override_status`、`override_remark`、`corrected_by`、`corrected_at`、`computed_at`；唯一键 `(tenant_id, user_id, work_date)` |

## 4. 规则

### 4.1 bpm 流程定义与版本

- 定义有草稿、发布、停用三态。草稿可编辑/删除；发布后不可编辑；停用后不可发起新实例，但在途实例继续。
- 同一 `key` 同时只有一个发布版本。发布新版本时，同 key 的旧发布版本自动转停用，二者在同一事务内完成。
- 实例在创建时锁定 `definition_id` 与 `definition_version`；后续定义变化不影响在途实例。
- 定义存在实例时不能删除，只能停用；草稿且无实例可以直接删除。
- 同一业务类型可以存在多个 `key`，但 v1 每个业务类型只使用一个默认 key，不实现按类别/金额路由。

### 4.2 审批人规则

- 节点按上表六种规则解析审批人；角色、权限码规则生成**候选池**，为每个候选人各建一条待办任务；任意一人办理后，同节点其余任务置为取消。
- 指定用户规则只生成一条任务；发起人、部门负责人规则解析出单个人。
- 部门负责人规则从目标部门开始沿 `parent_id` 向上查找 `leader_user_id > 0` 的部门；到根仍为空则失败。
- 候选人去重、排除停用用户；解析结果为空时提交失败并返回可读错误（400），不跳过节点、不自动通过。
- 允许发起人成为自己的审批人（自审批）；v1 不做自动跳过。

### 4.3 审批动作与状态机

| 动作 | 允许人 | 前置条件 | 结果 |
|---|---|---|---|
| 通过 | 当前任务办理人 | 任务待办 | 记录通过；若还有下一节点则创建下一节点任务，否则实例通过并回调业务 |
| 驳回 | 当前任务办理人 | 任务待办，原因必填 1–500 字 | 记录驳回；实例驳回；同实例所有待办取消；回调业务 |
| 撤回 | 发起人 | 实例运行中，且尚无任何通过/驳回记录 | 记录撤回；实例撤回；所有待办取消；回调业务 |
| 转办 | 当前任务办理人 | 任务待办，目标为同租户启用用户 | 当前任务转办关闭，为目标用户创建同节点新任务；记录转办；实例继续运行 |

- 任务状态：待办 → 通过/驳回/转办/取消；实例状态：运行中 → 通过/驳回/撤回。
- 并发：办理时对任务与实例加行锁；先办理者胜出，同节点兄弟任务取消；对非待办任务再次办理返回 409。
- 审批意见：驳回必填；通过、撤回、转办可选；长度 1–500 字。动作不附加附件。
- 每次提交（含驳回后重新提交）创建新实例；同一业务记录保留全部历史实例，详情页按时间倒序展示审批尝试。

### 4.4 业务回调

- bpm 定义 `BusinessHandler`：`OnApproved`、`OnRejected`、`OnWithdrawn`，入参包含事务、实例与驳回原因；业务模块实现并注册到 `business_type`。
- 实例通过/驳回/撤回时，bpm 在推进状态的同一事务内调用对应 handler；handler 失败则整笔回滚。
- 业务表保存自己的 `status`，是页面展示的事实来源；bpm 实例状态只服务待办与时间线。两者由同一事务保证一致。
- 启动时 `business_type` 没有注册 handler 的流程定义不允许发布/发起。

### 4.5 CRM 合同审批迁移

- 定义 `crm_contract_approval` v1：单节点「合同审批」，审批人规则为权限码 `crm:contract:approve`。
- 合同提交时，CRM 在事务内把合同置为待审并启动 bpm 实例；标题 `合同审批: {contractNo}`，摘要包含客户名称与金额。
- 通过 → 合同已签；驳回 → 草稿 + 驳回原因；撤回 → 草稿。合同页展示审批时间线与当前待办入口，不再内嵌审批按钮。
- 存量待审合同通过一次性幂等 backfill 补建实例；backfill 只处理「待审且无运行中实例」的合同，重复执行不产生重复实例。backfill 的实现入口（命令或启动任务）由实施工单决定。

### 4.6 数据权限

- bpm：待办/已办按 `assignee_id`；我发起按 `initiator_id`；定义管理按 `bpm:definition:*`；全量实例查询按 `bpm:instance:query`。
- OA 业务记录（请假、报销、考勤）统一按以下顺序取可见范围：
  1. 持有 `oa:<resource>:manage` → 全租户；
  2. 当前用户是某部门负责人 → 该部门子树内用户的数据（多个部门取并集）；
  3. 否则 → 仅本人。
- 公告按受众；日程按可见性；文档按目录 ACL；通知只看本人。
- 所有 OA/bpm/notify 表纳入租户插件；跨租户业务 ID 一律 404，不泄露存在性。

### 4.7 公告

- 受众：`audience_type=0` 全员；`audience_type=1` 时取所选部门子树与所选角色的并集，命中任一即可见。
- 状态：草稿 → 发布 → 撤回 → 草稿（可重新发布）；发布后不能删除，只能撤回；草稿可删除。
- 发布时在事务内为受众批量写入 `announcement` 通知；撤回不删除已写通知，用户打开详情看到「已撤回」。
- 打开详情即写已读记录（幂等）；列表显示未读标记；有 `oa:announcement:read-stats` 权限的人可看已读人数与名单。
- 置顶公告排在列表最前；富文本入库前用 bluemonday 白名单清洗，返回时仍是清洗后的 HTML。
- 附件挂 `infra_file`；草稿删除时软删除关联文件记录，不做物理删除。

### 4.8 请假

- 半天粒度：`startHalf`/`endHalf` 以 0=上午、1=下午表示；同一天 `下午→上午` 非法。
- 时长按「(日期, 半天) 槽位」计算：区间内每个工作日贡献上午/下午两个槽位，按起止边界裁剪；非工作日不贡献。总槽位为 0 时拒绝提交。
- 起止日期可以是休息日，但时长只计工作日；跨自然年的请假拒绝提交（v1 要求拆成两单）。
- 同一用户的待审/已通过请假按槽位判重；任何槽位重叠则拒绝提交。
- 额度：`balance_managed=true` 的类型检查可用额度 = `quota + adjustment - used`；不足则拒绝提交。通过时 `used += duration`；已通过请假在开始日期前取消时 `used -= duration`；驳回/撤回不动额度。
- 额度按自然年、按类型初始化；管理员可手工调整 `adjustment` 并填写原因，调整留痕在 `updater` 与备注字段。
- 状态：草稿 → 待审 → 通过/驳回；待审可撤回；通过后可在开始日期前取消；开始日期后由管理员直接调整请假记录。
- 通过/驳回/撤回由 bpm 回调更新状态；通过时同时触发受影响日期的考勤重算（考勤模块上线前为空操作钩子）。
- 默认流程 `oa_leave_approval`：节点 1 发起人所在部门负责人（向上查找），节点 2 权限码 `oa:leave:manage`（HR/管理员）。

### 4.9 报销

- 表头金额由明细行求和，服务端计算；请求中的表头金额忽略。
- 至少一条明细；每条金额 > 0；金额用 `Money`（int64 分 + decimal(12,2)）；类别取系统字典 `oa_expense_category`。
- 附件挂表头；状态草稿 → 待审 → 通过/驳回；驳回回草稿可修改重提；待审可撤回。
- 通过即终态，不做付款、预算、发票验真；审批通过后不允许修改明细。
- 默认流程 `oa_expense_approval`：节点 1 发起人所在部门负责人，节点 2 权限码 `oa:expense:manage`（财务/管理员）。

### 4.10 考勤

- 班次：一天一个时段，`end_time > start_time`，不支持跨天；`grace_minutes` 为迟到/早退宽限。
- 考勤组绑定一个班次；组员按用户物化，一个用户只能属于一个组；管理端支持按部门批量添加用户。用户没有考勤组时打卡返回 400。
- 打卡：上班/下班两种类型，Web 来源，记录 IP 与 User-Agent；同一天允许多次，结果取最早上班与最晚下班。打卡日期按 `app.timezone` 计算。
- 结果派生优先级：非工作日 → 休息；`leave_days >= 1` → 请假；缺上班卡或缺下班卡 → 缺卡；迟到且早退 → 迟到且早退；仅迟到 → 迟到；仅早退 → 早退；否则正常。`leave_days=0.5` 不改变状态，只在结果中记录半天请假。
- 管理员校正写 `override_status` + `override_remark`，不覆盖原始打卡；校正结果优先于派生结果，列表以标记区分。
- 重算触发：上班/下班打卡、请假通过/取消、管理员校正、每日定时任务、手工重算；重算按用户 + 日期范围执行。
- 工作日历：显式记录优先；无记录时周一至周五工作日、周六周日休息；支持将周末标记为工作日（调休上班）。
- 每日任务：`infra_job` 每天 01:00（应用时区）生成前一日结果；按租户迭代，系统级路径显式 `tenant.Skip`。
- 月度汇总：按用户/月聚合出勤天数、正常、迟到、早退、缺卡、请假天数；部门负责人看部门子树，HR/管理员看全租户。

### 4.11 日程与会议室

- 日程可见性：私有仅创建人；部门对本部门及子部门可见；公开对全租户可见，且创建需要 `oa:calendar:public` 权限。
- 部门日程的 `dept_id` 取创建人创建时的部门快照；用户调部门后历史日程仍归属原部门。
- 会议室预约校验时间范围与同会议室有效预约的时间重叠；冲突返回 400。
- 预约成功在同一事务内创建一条 `source=room_booking` 的日程，可见性为部门；取消预约同时取消日程。
- 会议室有未来有效预约时只能停用不能删除；预约本身只能取消不能删除。
- 会议提醒：`infra_job` 每 5 分钟扫描开始时间在 15 分钟后的有效预约，向预约人写 `meeting_reminder` 通知，`reminded_at` 保证只发一次。
- v1 不做参会人邀请、回执、重复日程、外部日历同步、预约审批。

### 4.12 文档

- 目录树为租户级；顶层目录必须显式设置至少一条 ACL，否则只有 owner/管理员可见。
- ACL 目标：全员（target_id=0）、部门（含子部门）、角色、用户；读权限按「最近一个有显式 ACL 的祖先目录」判定，子目录 ACL 覆盖父目录而不是叠加。
- 管理权 = 目录 owner 或持有 `oa:document:manage` 的用户；上传、改名、删除文件/子目录都需要目标目录管理权。
- 文件存 `infra_file`，`oa_document_file` 只存归属与展示字段；下载前校验目录读权限，不允许绕过 ACL 直接走文件 ID。
- 有子目录或文件的目录不能删除；文件删除为软删除；不做版本历史、Office 在线预览、协同编辑、跨目录移动。

### 4.13 通知

- 通知在触发事件的同一事务内批量写入，按接收人物化；写失败则源操作回滚。
- 类型：`bpm_todo`（新待办）、`bpm_result`（通过/驳回/撤回结果）、`announcement`（公告发布）、`meeting_reminder`（会议提醒）。
- 任务办理后，对应 `bpm_todo` 通知标记已读；同节点被取消的兄弟任务通知也标记已读。
- 未读数走 `(tenant_id, user_id, read_at)` 索引；前端每 60 秒轮询一次，不做 WebSocket。
- 通知内容包含标题、摘要、跳转路由与业务类型/ID；点击跳转到任务中心或业务详情。
- 不做邮件、短信、企微/钉钉；不做自动清理/归档。

### 4.14 删除与停用约束

| 对象 | 约束 |
|---|---|
| 流程定义 | 有实例只能停用；草稿且无实例可删除 |
| 请假类型/报销类别/班次/考勤组/会议室 | 被引用时只能停用，不能删除 |
| 请假/报销 | 仅草稿或已驳回可删除；待审/已通过不可删除 |
| 公告 | 发布后只能撤回，不能删除；草稿可删除 |
| 文档目录 | 有子目录或文件不能删除 |
| 会议室预约 | 只能取消，不能删除 |

## 5. API 清单

统一前缀 `/admin-api`，沿用 `{code,data,msg}`、`pageNo/pageSize`、`Authorization: Bearer`。下表只列资源与动作，具体字段在实施时按现有 CRM 风格定义。

### bpm

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/bpm/definition/page` | `bpm:definition:query` |
| GET | `/bpm/definition/{id}` | `bpm:definition:query` |
| POST | `/bpm/definition` | `bpm:definition:create` |
| PUT | `/bpm/definition` | `bpm:definition:update`，仅草稿 |
| DELETE | `/bpm/definition/{id}` | `bpm:definition:delete`，仅草稿无实例 |
| POST | `/bpm/definition/{id}/publish` | `bpm:definition:publish` |
| POST | `/bpm/definition/{id}/disable` | `bpm:definition:publish` |
| GET | `/bpm/task/page` | `bpm:task:query`，tab=todo/done/initiated |
| GET | `/bpm/instance/{id}` | 发起人/办理人/`bpm:instance:query` |
| POST | `/bpm/task/{id}/approve` | 当前办理人 |
| POST | `/bpm/task/{id}/reject` | 当前办理人，原因必填 |
| POST | `/bpm/task/{id}/transfer` | 当前办理人，目标用户 |
| POST | `/bpm/instance/{id}/withdraw` | 发起人，首个审批前 |
| GET | `/bpm/instance/page` | `bpm:instance:query` |

### notify

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/notify/message/page` | `notify:message:query`，仅本人 |
| GET | `/notify/message/unread-count` | 仅本人 |
| PUT | `/notify/message/{id}/read` | 仅本人 |
| PUT | `/notify/message/read-all` | 仅本人 |

### OA 公告

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/oa/announcement/page` | `oa:announcement:query`，管理列表 |
| GET | `/oa/announcement/my/page` | 登录用户，按受众过滤 |
| GET | `/oa/announcement/{id}` | 可见用户；打开即已读 |
| POST | `/oa/announcement` | `oa:announcement:create` |
| PUT | `/oa/announcement` | `oa:announcement:update`，仅草稿 |
| DELETE | `/oa/announcement/{id}` | `oa:announcement:delete`，仅草稿 |
| POST | `/oa/announcement/{id}/publish` | `oa:announcement:publish` |
| POST | `/oa/announcement/{id}/withdraw` | `oa:announcement:publish` |
| GET | `/oa/announcement/{id}/read-stats` | `oa:announcement:read-stats` |

### OA 请假

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/oa/leave/type/list` | 登录用户 |
| POST/PUT/DELETE | `/oa/leave/type...` | `oa:leave:type:manage` |
| GET | `/oa/leave/balance/page` | `oa:leave:balance:adjust` 或本人 |
| POST | `/oa/leave/balance/adjust` | `oa:leave:balance:adjust` |
| GET | `/oa/leave/request/page` | `oa:leave:query`，按 4.6 数据权限 |
| GET | `/oa/leave/request/{id}` | 可见用户 |
| POST | `/oa/leave/request` | `oa:leave:create` |
| PUT | `/oa/leave/request` | `oa:leave:update`，仅草稿/驳回 |
| DELETE | `/oa/leave/request/{id}` | `oa:leave:delete`，仅草稿/驳回 |
| POST | `/oa/leave/request/{id}/submit` | `oa:leave:submit` |
| POST | `/oa/leave/request/{id}/withdraw` | 发起人，首个审批前 |
| POST | `/oa/leave/request/{id}/cancel` | 发起人，已通过且开始日期前 |

### OA 报销

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/oa/expense/claim/page` | `oa:expense:query`，按 4.6 数据权限 |
| GET | `/oa/expense/claim/{id}` | 可见用户 |
| POST | `/oa/expense/claim` | `oa:expense:create` |
| PUT | `/oa/expense/claim` | `oa:expense:update`，仅草稿/驳回 |
| DELETE | `/oa/expense/claim/{id}` | `oa:expense:delete`，仅草稿/驳回 |
| POST | `/oa/expense/claim/{id}/submit` | `oa:expense:submit` |
| POST | `/oa/expense/claim/{id}/withdraw` | 发起人，首个审批前 |

### OA 日程与会议室

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/oa/calendar/event/list` | `oa:calendar:query`，按可见性 |
| POST | `/oa/calendar/event` | `oa:calendar:create`；公开需 `oa:calendar:public` |
| PUT | `/oa/calendar/event` | `oa:calendar:update` |
| DELETE | `/oa/calendar/event/{id}` | `oa:calendar:delete` |
| GET | `/oa/room/list` | `oa:room:query` |
| POST/PUT/DELETE | `/oa/room...` | `oa:room:manage` |
| GET | `/oa/room/booking/list` | `oa:room:query` |
| POST | `/oa/room/booking` | `oa:room:booking` |
| POST | `/oa/room/booking/{id}/cancel` | 预约人/`oa:room:manage` |

### OA 文档

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| GET | `/oa/document/folder/tree` | `oa:document:query`，按 ACL 可见 |
| POST/PUT/DELETE | `/oa/document/folder...` | 目录管理权 |
| GET | `/oa/document/file/page` | 目录读权限 |
| POST | `/oa/document/file/upload` | 目录管理权 |
| GET | `/oa/document/file/{id}/download` | 目录读权限 |
| DELETE | `/oa/document/file/{id}` | 目录管理权 |

### OA 考勤

| 方法 | 路径 | 权限/说明 |
|---|---|---|
| POST | `/oa/attendance/punch` | `oa:attendance:punch`，需有考勤组 |
| GET | `/oa/attendance/result/my` | 本人 |
| GET | `/oa/attendance/result/page` | `oa:attendance:query`，按 4.6 |
| POST | `/oa/attendance/result/{id}/override` | `oa:attendance:manage` |
| GET | `/oa/attendance/summary` | `oa:attendance:summary:query`，按 4.6 |
| POST/PUT/DELETE | `/oa/attendance/shift...` | `oa:attendance:shift:manage` |
| POST/PUT/DELETE | `/oa/attendance/group...` | `oa:attendance:group:manage` |
| POST/PUT/DELETE | `/oa/attendance/calendar...` | `oa:attendance:calendar:manage` |

## 6. 前端页面

| 页面 | 内容 |
|---|---|
| bpm 流程定义 | 列表、草稿编辑（有序节点、审批人规则）、发布、停用、版本历史 |
| bpm 待办任务 | 待办/已办/我发起三个 tab；按业务类型、时间筛选；详情含业务摘要与审批时间线 |
| 通知中心 | 顶栏铃铛 + 未读数；列表、标记已读/全部已读、跳转 |
| 公告 | 管理列表、草稿编辑（富文本 + 受众 + 附件 + 置顶）、发布/撤回、已读统计；我的公告列表与详情 |
| 请假 | 我的请假、类型管理、额度管理、表单（日期 + 上午/下午 + 事由 + 附件）、详情与时间线 |
| 报销 | 我的报销、表头 + 明细行编辑、附件、详情与时间线 |
| 考勤 | 打卡卡片、我的考勤、部门/全租户考勤列表、月度汇总、班次管理、考勤组管理、工作日历、校正弹窗 |
| 日程 | 月/周视图 + 列表；新建私有/部门/公开日程 |
| 会议室 | 会议室管理、按房间/日期查看预约、预约与取消、冲突提示 |
| 文档 | 目录树 + 文件列表、上传/下载、目录 ACL 设置 |

- 富文本使用 wangEditor（Vue 3）；服务端 bluemonday 清洗。
- 日程/会议室用 Element Plus 日历与列表自建，不引入 FullCalendar。
- 上传沿用 CRM 合同页的 `el-upload` + infra file 模式，抽成 OA 共用上传组件。
- 页面沿用 ProTable 的列/搜索/表单配置；动态路由与按钮权限由后端菜单下发。

## 7. 迁移号、菜单、权限码、种子

### 7.1 迁移

| 迁移 | 内容 | 新增表数 |
|---|---|---|
| `00014_bpm_core` | 流程定义、实例、任务、审批记录 | 4 |
| `00015_notify_message` | 站内通知 | 1 |
| `00016_oa_announcement` | 公告、受众、已读 | 3 |
| `00017_oa_workday_calendar` | 工作日历（请假/考勤共用） | 1 |
| `00018_oa_leave` | 请假类型、额度、申请 | 3 |
| `00019_oa_expense` | 报销单、明细 | 2 |
| `00020_oa_calendar_room` | 日程、会议室、预约 | 3 |
| `00021_oa_document` | 目录、ACL、文件 | 3 |
| `00022_oa_attendance` | 班次、考勤组、组员、打卡、结果 | 5 |

迁移完成后 `verify-migration.sh` 的 `EXPECTED_TABLES` 从 32 调整为 **57**（以实际迁移为准；新增表共 25）。CRM 合同审批迁移不需要新表；存量合同 backfill 不引入 DDL。

### 7.2 菜单与权限码

| 段 | 菜单 | 权限码 |
|---|---|---|
| 500 | OA 根目录 | — |
| 501 | 公告 | `oa:announcement:view/query/create/update/delete/publish/read-stats` |
| 503 | 请假 | `oa:leave:query/create/update/delete/submit/cancel`、`oa:leave:type:manage`、`oa:leave:balance:adjust`、`oa:leave:manage` |
| 504 | 报销 | `oa:expense:query/create/update/delete/submit`、`oa:expense:manage` |
| 505 | 考勤 | `oa:attendance:punch/query`、`oa:attendance:manage`、`oa:attendance:shift:manage`、`oa:attendance:group:manage`、`oa:attendance:calendar:manage`、`oa:attendance:summary:query` |
| 506 | 日程 | `oa:calendar:query/create/update/delete`、`oa:calendar:public` |
| 507 | 会议室 | `oa:room:query/manage`、`oa:room:booking` |
| 508 | 文档 | `oa:document:query/upload/manage` |
| 510 | bpm 根目录 | — |
| 511 | 流程定义 | `bpm:definition:query/create/update/delete/publish` |
| 512 | 待办任务 | `bpm:task:query/handle`、`bpm:instance:query` |

按钮 ID 在 5000–5299 段内按模块取号（5011+ 公告、5031+ 请假、5041+ 报销、5051+ 考勤、5061+ 日程、5071+ 会议室、5081+ 文档、5111+ 流程定义、5121+ 待办）。新工单先 `grep` 去重再取号。

### 7.3 种子

- 流程定义：`crm_contract_approval` v1（权限码 `crm:contract:approve`）；`oa_leave_approval` v1（发起人部门负责人 → `oa:leave:manage`）；`oa_expense_approval` v1（发起人部门负责人 → `oa:expense:manage`）。
- 请假类型：年假（额度 5，占额度）、事假（不占额度）、病假（不占额度）、调休（占额度，0）、婚假（3）、产假（98）、陪产假（15）。额度可由管理员调整。
- 报销类别字典 `oa_expense_category`：交通、餐饮、住宿、办公、其他。
- 默认班次 09:00–18:00，宽限 5 分钟；默认考勤组。
- 菜单与权限码写入 `seedMenus`；内置普通角色授予公告查看、请假/报销/考勤/日程/文档的基础菜单，管理员角色全量。
- `seed-demo` 写入 OA 演示数据：公告、请假申请与额度、报销单、打卡与考勤结果、日程、会议室预约、文档目录与文件；仅当对应表为空时写入，重复启动不重复插入。

## 8. 测试矩阵

### 8.1 Seam

- **主 seam：HTTP API + 真实 MySQL**。复用现有 testutil/router/JWT/权限/租户中间件，测试库独立，用例间清表；不引入测试专用后门。
- **次 seam：纯函数表驱动测试**。审批人解析、请假槽位/时长、考勤结果派生、文档 ACL 继承、状态机、报销金额合计。
- **跨模块 seam：`bpm.BusinessHandler`**。bpm 测试用 fake handler；真实 handler 通过 CRM/OA 的 HTTP 集成测试覆盖。
- **前端**：不新增组件测试框架；`pnpm type-check` + `pnpm build` + 手工冒烟。

### 8.2 重点场景

| 领域 | 场景 |
|---|---|
| bpm | 发布新版本停用旧版本；在途实例锁版本；候选池两人先办者胜出、兄弟任务取消；部门负责人向上查找；无审批人 400；驳回/撤回/转办；跨租户 404；handler 失败回滚 |
| CRM | 合同提交建实例；审批通过/驳回/撤回更新合同；存量待审合同 backfill 幂等 |
| notify | 待办/结果/公告/会议提醒扇出；未读数；标记已读；任务处理后待办通知已读；租户隔离 |
| 公告 | 全员/部门子树/角色并集可见；已读幂等；发布通知；撤回后不可见；置顶排序；富文本 XSS 清洗 |
| 请假 | 半天/跨周末/节假日/调休工作日时长；槽位重叠；余额不足；通过扣减/取消返还；跨年拒绝；部门负责人可见；租户隔离 |
| 报销 | 明细合计；无明细/负金额拒绝；审批通过终态；财务可见；租户隔离 |
| 考勤 | 无考勤组打卡 400；迟到/早退/缺卡；请假整天/半天；休息日与调休上班；管理员校正优先；月度汇总；请假通过/取消触发重算；租户隔离 |
| 日程/会议室 | 私有/部门/公开可见性；公开创建权限；同会议室时间冲突；预约自动生成日程；取消同步；提醒只发一次 |
| 文档 | 顶层目录必须 ACL；最近祖先继承；部门子树/角色/用户/全员；下载鉴权；有子目录不能删除；租户隔离 |
| 迁移/种子 | 空库迁移往返；`EXPECTED_TABLES=57`；seed 幂等；seed-demo 幂等 |

### 8.3 验收命令

```bash
make check
make test-integration
make verify-migration
make web-type-check
make web-build
```

## 9. 验收标准

- bpm 内核与 notify 可用；CRM 合同审批完整走 bpm，存量待审合同 backfill 后无遗漏。
- 公告、请假、报销、日程、会议室、文档、考勤全部有可操作页面与 HTTP API。
- 所有列表遵守 4.6 数据权限；所有表租户隔离；所有审批动作有不可变记录。
- `make check`、`make test-integration`、`make verify-migration`、`make web-type-check`、`make web-build` 全绿。
- README、GLOSSARY、ADR、迁移说明与代码一致；新增依赖（wangEditor、bluemonday）写入依赖清单。
- 现有 CRM 功能无回归（金额不变量、数据权限、Excel、公海、仪表盘）。

## 10. 暂缓 / 开放问题

- **暂缓**：BPMN 网关/并行/会签/条件表达式/可视化设计器；动态表单引擎；公告定时发布、邮件/短信/企微；请假按小时、额度累计/结转、跨年请假；报销付款/预算/发票；考勤 GPS/设备/加班/跨天班次/员工补卡申请；日程重复/外部同步/参会人邀请；会议室预约审批；文档版本/在线预览/协同编辑；OA 移动端与 OpenAPI。
- **开放问题**：
  - 自审批：v1 允许发起人成为自己的审批人，不做自动跳过；若需要排除，后续在审批人规则中增加「排除发起人」开关。
  - 存量合同 backfill 的实现入口（命令 vs 启动任务）由实施工单决定，要求幂等。
  - 考勤组是否支持按部门自动同步新员工：v1 只做按部门批量添加一次，不做持续同步。
  - 公告已读统计是否需要对未读用户催办：v1 不做。
