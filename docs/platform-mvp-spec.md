# 平台 MVP 详细规格：消息 / 社交登录 / 实时推送 / 租户套餐 / 治理与可观测

- 状态：ready-for-agent
- 父 issue：待建（本文件为规则唯一来源）
- 相关 ADR：0010 消息通道、0011 社交登录、0012 租户套餐与配额、0013 实时推送、0014 访问日志；0009 的通知边界被 0010/0013 部分取代
- 前置里程碑：CRM MVP（#1–#21）、OA MVP（#42–#63）均已合入 `main`

## 1. 范围与施工顺序

本里程碑补齐后续业务套件（mall/erp/pay/mp/member/ai）之前必须存在的平台能力，一次交付九块：

1. 租户套餐与配额（含菜单授权与权限语义调整）
2. 消息通道（短信 + 邮件）
3. 第三方社交登录（微信开放平台 / 钉钉 / 企微）
4. 实时推送（SSE + WebSocket，共用 hub）
5. API 访问日志
6. 敏感词
7. 地区库与 IP 定位
8. 数据库文档
9. 监控面板

施工顺序按依赖分批：

```
规格/ADR → 租户套餐（模型 → 绑定与权限 → 用户/存储配额）
        → 消息（骨架 → 邮件/短信 → 月度配额）
        → 社交登录（骨架 → 三家 provider + 前端）
        → 推送（SSE → WebSocket）
        → 治理与可观测（访问日志/敏感词/地区 IP/DB 文档 → 监控）
        → 收尾（演示、文档、验收、审查、PR）
```

## 2. 模块边界与依赖

| 模块 | 职责 | 依赖 | 不负责 |
|---|---|---|---|
| `system` | 租户套餐、权限语义、社交登录、敏感词、地区 | `pkg/jwt`、`pkg/tenant`、`pkg/datascope` | 外部消息投递、推送传输 |
| `infra` | 访问日志、数据库文档、监控、上传配额钩子 | `pkg/dbschema`、`pkg/iploc` | 业务通知、租户套餐定义 |
| `message`（新） | 短信/邮件渠道、模板、发送、发送日志 | `system` 的配额网关 | 站内通知、推送 |
| `notify` | 站内通知 + 推送票据与 SSE/WS 端点 | `pkg/push`、`message`（不直接调用） | 外部通道投递 |
| `pkg/push` | 连接 hub、一次性票据、统一事件模型 | 无 | 认证、业务事件生产 |
| `pkg/iploc` | IP 定位接口与可选 xdb 驱动 | 无 | 登录日志存储 |
| `pkg/dbschema` | `information_schema` 读取与表结构模型 | 无 | 文档渲染、代码生成 |

依赖方向：`message → system`（只通过 `PackageQuota` 窄接口）；`notify → pkg/push`；`infra → pkg/dbschema/pkg/iploc`；任何模块不反向依赖 `message`/`notify`。跨模块回调在 `internal/app` 装配。

## 3. 表结构

通用底座遵循 ADR 0002：`id/created_at/updated_at/deleted_at/creator/updater`；`creator/updater` 为 `varchar(64)`。append-only 日志表不设 `deleted_at`，并在注释中说明。

### 3.1 `system_tenant_package`（平台级，迁移 00023）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | bigint PK | |
| `name` | varchar(100) | 套餐名，唯一（含软删维度） |
| `status` | tinyint | 0 启用 / 1 停用 |
| `menu_ids` | text | JSON 数组，只存目录/菜单 ID；按钮由服务端按父菜单展开 |
| `max_users` | bigint | 最大用户数，0 = 不限 |
| `max_storage_mb` | bigint | 存储上限 MB，0 = 不限 |
| `max_sms_monthly` | bigint | 每月短信条数，0 = 不限 |
| `max_mail_monthly` | bigint | 每月邮件封数，0 = 不限 |
| `remark` | varchar(500) | |

`system_tenant` 增加 `package_id bigint NOT NULL DEFAULT 0` + 索引 `idx_system_tenant_package_id`。平台租户（ID=1）固定 `package_id=0`。

### 3.2 `message_channel`（平台级，迁移 00024）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` / 底座 | | |
| `channel_type` | varchar(16) | `sms` / `mail` |
| `code` | varchar(64) | 渠道编码，唯一 |
| `name` | varchar(100) | |
| `provider` | varchar(64) | `log` / `smtp` / `aliyun_sms` |
| `config` | text | provider 配置 JSON，密钥字段查询时脱敏 |
| `status` | tinyint | 0 启用 / 1 停用 |
| `remark` | varchar(500) | |

### 3.3 `message_template`（平台级，迁移 00024）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` / 底座 | | |
| `channel_type` | varchar(16) | `sms` / `mail` |
| `code` | varchar(64) | 模板编码，`(channel_type, code)` 唯一 |
| `name` | varchar(100) | |
| `title` | varchar(200) | 邮件主题；短信为空 |
| `content` | text | 短信模板内容或邮件 HTML/纯文本 |
| `params` | varchar(1000) | JSON 数组，声明参数名 |
| `status` | tinyint | 0 启用 / 1 停用 |
| `remark` | varchar(500) | |

### 3.4 `message_log`（租户级，append-only，迁移 00024）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | bigint PK | |
| `created_at` | datetime(3) | |
| `tenant_id` | bigint | 租户插件隔离 |
| `channel_type` | varchar(16) | |
| `channel_id` | bigint | |
| `template_code` | varchar(64) | |
| `recipient` | varchar(255) | 手机号或邮箱 |
| `title` | varchar(200) | 邮件主题 |
| `content` | text | 渲染后内容（截断 4000） |
| `status` | tinyint | 0 成功 / 1 失败 |
| `error_message` | varchar(1000) | |
| `provider_message_id` | varchar(128) | |
| `retry_count` | int | 手动重发次数 |
| `sent_at` | datetime(3) | |

索引 `(tenant_id, created_at)`、`(status)`。

### 3.5 `system_social_client`（平台级，迁移 00025）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` / 底座 | | |
| `code` | varchar(64) | 客户端编码，唯一 |
| `name` | varchar(100) | |
| `provider` | varchar(32) | `wechat_open` / `dingtalk` / `wecom` |
| `client_id` | varchar(200) | |
| `client_secret` | varchar(500) | 查询脱敏 |
| `authorize_url` | varchar(500) | 为空时用 provider 默认值 |
| `token_url` | varchar(500) | |
| `userinfo_url` | varchar(500) | |
| `scopes` | varchar(200) | 空格分隔 |
| `redirect_uri` | varchar(500) | 必须与回调端点一致 |
| `status` | tinyint | 0 启用 / 1 停用 |
| `remark` | varchar(500) | |

### 3.6 `system_social_user`（租户级，迁移 00025）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` / 底座 | | |
| `tenant_id` | bigint | |
| `user_id` | bigint | 绑定的本地用户 |
| `provider` | varchar(32) | |
| `openid` | varchar(128) | |
| `unionid` | varchar(128) | 可空 |
| `nickname` | varchar(100) | |
| `avatar` | varchar(500) | |
| `raw_info` | text | 三方原始信息，截断 4096 |
| `status` | tinyint | 0 启用 |
| `last_login_at` | datetime(3) | |

唯一键 `(tenant_id, provider, openid, deleted_at)`；索引 `(tenant_id, user_id)`。

### 3.7 `infra_api_log`（租户级，append-only，迁移 00026）

`id`、`created_at`、`tenant_id`、`user_id`、`username`、`request_method`、`request_path`（路由模板）、`request_query`（脱敏截断 1024）、`response_status`、`duration_ms`、`client_ip`、`user_agent`（截断 500）、`request_id`。索引 `(tenant_id, created_at)`、`(request_path)`、`(response_status)`。

### 3.8 `system_sensitive_word`（租户级，迁移 00027）

`id` / 底座、`tenant_id`、`word varchar(100)`、`category varchar(50)`、`status tinyint`、`remark`。唯一键 `(tenant_id, word, deleted_at)`。

### 3.9 `system_area`（平台级，迁移 00028）

`id`、`created_at`、`updated_at`、`code varchar(12)`、`parent_code varchar(12)`、`name varchar(100)`、`level tinyint`（1 省 / 2 市 / 3 区县）、`sort int`、`status tinyint`。唯一键 `code`；索引 `parent_code`。参考数据不设软删与 `creator/updater`。

## 4. 规则

### 4.1 租户套餐与菜单授权

1. 套餐 CRUD、停用、删除仅平台租户（ID=1）可操作；删除时若存在引用该套餐的租户，返回 409。
2. `menu_ids` 校验：每个 ID 必须存在且 `type in (目录, 菜单)`；目录选中时其子菜单由前端树组件一并提交，不做隐式补全；按钮不在 `menu_ids` 中存储，绑定套餐时按父菜单自动展开（只展开启用按钮）。
3. 绑定套餐（创建/修改租户、单独换套餐）在同一事务内：
   - 更新 `system_tenant.package_id`；
   - 删除该租户所有 `code='super_admin'` 角色的 `system_role_menu` 行；
   - 插入套餐展开后的菜单/按钮行，`tenant_id` 显式写目标租户；
   - 清空全量权限缓存。
4. 新建租户未指定套餐时，绑定全部启用菜单（保持旧行为）；指定套餐时按套餐绑定。
5. **权限语义**：
   - 平台租户（ID=1）的 `super_admin` 仍返回通配权限 `*`，`UserInfo` 返回全部启用非按钮菜单，数据范围强制「全部」。
   - 其他租户的 `super_admin` 不再通配：权限码与菜单来自 `system_role_menu`；数据范围按角色 `data_scope`（租户引导时写入「全部」）。
   - 迁移 00023 为存量租户（ID>1）的 `super_admin` 回填全部菜单，避免升级后失权。
6. 权限缓存继续按 `userID` 1 分钟 TTL；套餐变更时调用 `ClearAllPermissionCache`。

### 4.2 配额

1. 配额对象：`MaxUsers`、`MaxStorageMB`、`MaxSMSMonthly`、`MaxMailMonthly`；0 表示不限。平台租户永远不限。
2. 用户数：`CreateUser` 前统计该租户未删除用户（含停用）总数，`>= MaxUsers` 返回 409「租户用户数已达套餐上限」。
3. 存储：文件上传前统计 `infra_file` 未删除记录的 `size` 之和，加上本次上传后超过 `MaxStorageMB * 1024 * 1024` 返回 409「租户存储空间不足」。
4. 短信/邮件：发送前按应用时区统计当月 `message_log` 中对应 `channel_type` 的行数，`>= Max*Monthly` 返回 409「本月短信/邮件额度已用尽」。
5. 用量查询返回 `limit` 与 `used` 两组值；平台租户返回 `unlimited=true`。
6. 配额校验失败不得写入业务数据；消息发送失败（provider 错误）仍写日志。

### 4.3 消息通道

1. 渠道/模板是平台级配置；租户只能发送与查看自己的发送日志。读取渠道/模板时显式 `tenant.Skip`，写日志使用当前租户上下文。
2. 发送流程：校验模板存在且启用 → 校验渠道启用 → 校验参数与模板声明完全一致 → 渲染 → 配额校验 → provider 发送（超时 10s）→ 写日志。
3. 模板渲染只支持 `{{name}}` 占位；参数名匹配 `^[A-Za-z_][A-Za-z0-9_]*$`；缺失或多余参数返回 400。
4. 短信收件人校验 `^1[3-9]\d{9}$`；邮件校验基础 RFC 5322 格式。
5. provider 错误返回 502 与中文提示，错误详情只落日志，不回显密钥。
6. 密钥字段（`password`、`secret`、`accessKeySecret`）查询时替换为 `******`；更新时提交 `******` 表示保持原值。
7. 手动重发只允许失败日志；重发更新原行的 `status/error_message/provider_message_id/sent_at/retry_count`，不新增行。
8. 本轮不做自动重试、批量发送、模板变量类型校验、附件。

### 4.4 社交登录

1. 客户端配置仅平台租户可管理；登录页只展示启用的客户端。
2. `authorize-url`（公开）参数 `provider`、`tenantId`（缺省 1）；生成 32 字节随机 state 与 PKCE `code_verifier`，服务端保存 10 分钟、一次性；返回 `{url, state}`。
3. 回调（公开）校验 state 存在、未过期、未使用、provider 与客户端匹配；用 `code` 换 token、拉用户信息。
4. 已绑定 `(tenant_id, provider, openid)`：签发 JWT，写 `system_login_log`，`login_type=3`。
5. 未绑定：返回 409，业务码 `1901`，消息「该社交账号尚未绑定系统用户，请先用账号密码登录后在个人中心绑定」；**不自动建号**。
6. 绑定：已登录用户调用 `POST /system/social/bind`，state 携带 `user_id`；回调写入 `system_social_user`；同一 openid 已绑定其他用户返回 409；同一用户重复绑定同一 openid 幂等成功。
7. 解绑：只能解绑本人；管理员可通过平台接口解绑指定租户用户；不存在/跨租户 404。
8. 三个 provider 的默认端点：微信开放平台扫码、钉钉 OAuth2、企微扫码登录；配置了自定义端点时以配置为准。三方 HTTP 调用超时 10s。
9. `raw_info` 截断 4096 字节；不存三方 access token。

### 4.5 实时推送

1. `push.Hub` 按 `(tenant_id, user_id)` 订阅；同用户多连接全部收到；单连接发送缓冲满时丢弃该事件并计数，不阻塞发布方。
2. `push.Ticket`：32 字节随机值，base64url；TTL 30 秒；一次性消费；绑定 tenant/user。票据只存进程内。
3. 端点：
   - `POST /notify/push/ticket`（Bearer）→ `{ticket, expiresIn, transports}`；
   - `GET /notify/push/sse?ticket=`（公开，凭票据）→ `text/event-stream`，每 25 秒心跳注释；
   - `GET /notify/push/ws?ticket=`（公开，凭票据）→ WebSocket，服务端只发不收（客户端 ping 除外）。
4. 事件格式：

```json
{"type":"notification","data":{"title":"...","content":"...","type":"bpm_todo","bizType":"bpm_task","bizId":1,"route":"/bpm/todo","routeParams":"{\"taskId\":1}"},"ts":1730000000}
```

5. 发布时机：`notify.Create` 在自开事务提交后发布；`notify.CreateInTx` 的生产者在事务提交后调用 `Publish`。首批接入 bpm 待办、公告发布、会议提醒；其余生产者继续依赖轮询。推送是 best-effort，事务回滚产生的瞬时事件以前端刷新为准。
6. 前端传输优先级：WebSocket → SSE → 60 秒轮询。断线重连退避 1/2/5/10/30 秒；任一传输失败自动降级。
7. 单实例限制：hub 与票据均在进程内，多实例部署时其他实例的连接收不到事件，需要外部 pub/sub；本轮不实现，写入 ADR。

### 4.6 API 访问日志

1. 只记录受保护接口（`Auth` 之后）；公开接口（登录、验证码、社交回调、推送流）不记录。
2. 字段：路由模板 `c.FullPath()`（取不到时用原始 path）、脱敏 query、状态码、耗时、IP、UA、request_id。
3. `request_id`：优先读取请求头 `X-Request-Id`（限 64 字符），否则生成 UUID；写回响应头。
4. 脱敏 query 参数名 `token`、`access_token`、`password`、`secret`、`refresh_token`；query 整体截断 1024，UA 截断 500。
5. 异步写：内存队列默认 4096，批量 200 条或 1 秒落库；队列满丢弃并每 100 条告警一次；`Flush(ctx)` 供测试与优雅关闭。
6. 写日志失败绝不影响请求；不记录请求体。
7. 保留期默认 30 天；系统级 cron `cleanAccessLogs` 每日 03:00 清理，跨租户 `tenant.Skip`。

### 4.7 敏感词

1. 词库租户级；单词 1–50 字符，去空白后非空；唯一 `(tenant_id, word)`；单租户上限 5000 条。
2. 匹配用 rune trie，ASCII 不区分大小写；返回去重后的命中词（按首次出现位置排序）与打码文本（命中字符替换为 `*`）。
3. 缓存按租户保存，CRUD 后失效；停用词不参与匹配。
4. 接入点：OA 公告创建/修改（标题 + 正文）、CRM 跟进创建/修改（内容）。命中返回 400「内容包含敏感词：a、b」。
5. 其他模块通过 `SensitiveWordChecker` 接口调用，由 `internal/app` 注入；未注入时不做检查（测试基座可显式关闭）。

### 4.8 地区与 IP 定位

1. `system_area` 为平台级参考数据；所有登录用户可读，只有平台租户可导入。
2. 种子在表为空时写入省 + 市两级，幂等；区县通过导入接口补齐。导入 JSON 数组，按 `code` upsert，单次最多 5000 条，父节点必须已存在（或同批出现），层级 1/2/3。
3. 树接口按 `parent_code` 返回子节点；`parentCode` 为空返回省级。
4. IP 定位：`iploc.Resolver` 接口；配置 `ip-location.xdb-path` 时启用 ip2region xdb，否则空实现返回空字符串。文件不存在/加载失败只告警一次，不影响启动。
5. 登录日志分页在查询时补 `loginLocation`，带 10000 条 LRU 缓存；定位失败返回空，不影响分页。

### 4.9 数据库文档

1. 读取 `information_schema`，覆盖全部业务表（含 `system_*`、`infra_*`），排除 `goose_db_version`。
2. 列表支持关键字过滤；详情返回列名、类型、可空、主键、自增、默认值、注释、序号。
3. Markdown 导出包含生成时间、数据库名、每张表的说明与列表；文件名 `db-doc-YYYYMMDD-HHmmss.md`。
4. 仅平台租户可访问。

### 4.10 监控面板

1. `overview` 返回：应用名/环境/版本/Go 版本/启动时间/运行时长；goroutine 数、堆内存、GC 次数；DB 连接池 open/in-use/idle/wait 与上限；调度器任务总数/启用数/注册 handler 数；租户数/用户数；近 24h 访问量、错误率、平均与最大耗时；近 24h 短信/邮件发送成功/失败数。
2. `requests` 按小时聚合最近 1–168 小时的请求数、2xx/3xx/4xx/5xx、平均耗时、最大耗时。
3. 仅平台租户可访问；不做 Prometheus、pprof、链路追踪。

### 4.11 密钥与安全

1. 消息渠道配置、社交客户端密钥在 DB 明文存储（无 KMS），API 查询一律脱敏；更新时 `******` 表示保留原值。
2. 访问日志与操作日志都不得记录密钥/密码/token；推送票据不得写入日志。
3. 社交回调、推送流端点必须校验 state/ticket，失败一律 401/400，不泄露内部错误。
4. 所有新租户表必须有跨租户 404 集成用例；平台级表不得加入 `TenantTables`，系统级读取显式 `tenant.Skip`。

## 5. API 清单

### 5.1 租户套餐

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/system/tenant-package/page` | `system:tenant-package:query` |
| GET | `/system/tenant-package/simple-list` | `system:tenant-package:query` |
| GET | `/system/tenant-package/:id` | `system:tenant-package:query` |
| POST | `/system/tenant-package` | `system:tenant-package:create` |
| PUT | `/system/tenant-package/:id` | `system:tenant-package:update` |
| PUT | `/system/tenant-package/:id/status` | `system:tenant-package:update` |
| DELETE | `/system/tenant-package/:id` | `system:tenant-package:delete` |
| GET | `/system/tenant/quota` | 登录即可（当前租户） |
| GET | `/system/tenant/:id/quota` | `system:tenant:query`（平台） |

租户创建/更新 DTO 增加 `packageId`。

### 5.2 消息

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/message/channel/page` | `message:channel:query` |
| POST/PUT/DELETE | `/message/channel[/:id]` | `message:channel:create/update/delete` |
| PUT | `/message/channel/:id/status` | `message:channel:update` |
| POST | `/message/channel/:id/test` | `message:channel:update` |
| GET | `/message/template/page` | `message:template:query` |
| POST/PUT/DELETE | `/message/template[/:id]` | `message:template:create/update/delete` |
| POST | `/message/sms/send` | `message:sms:send` |
| POST | `/message/mail/send` | `message:mail:send` |
| GET | `/message/log/page` | `message:log:query` |
| POST | `/message/log/:id/resend` | `message:log:resend` |
| DELETE | `/message/log` / `/message/log/clear` | `message:log:delete` |

### 5.3 社交登录

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/system/social/client/page` | `system:social:query` |
| POST/PUT/DELETE | `/system/social/client[/:id]` | `system:social:create/update/delete` |
| GET | `/system/social/authorize-url` | 公开 |
| GET | `/system/social/callback` | 公开 |
| POST | `/system/social/bind` | 登录即可 |
| GET | `/system/social/user/page` | `system:social-user:query` |
| DELETE | `/system/social/user/:id` | `system:social-user:delete` |

### 5.4 推送

| 方法 | 路径 | 权限 |
|---|---|---|
| POST | `/notify/push/ticket` | 登录即可 |
| GET | `/notify/push/sse` | 票据 |
| GET | `/notify/push/ws` | 票据 |

### 5.5 访问日志

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/infra/api-log/page` | `infra:api-log:query` |
| DELETE | `/infra/api-log` | `infra:api-log:delete` |
| DELETE | `/infra/api-log/clear` | `infra:api-log:delete` |

### 5.6 敏感词

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/system/sensitive-word/page` | `system:sensitive-word:query` |
| POST/PUT/DELETE | `/system/sensitive-word[/:id]` | `system:sensitive-word:create/update/delete` |
| POST | `/system/sensitive-word/check` | `system:sensitive-word:query` |

### 5.7 地区

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/system/area/tree` | `system:area:query` |
| GET | `/system/area/children` | `system:area:query` |
| POST | `/system/area/import` | `system:area:import`（平台） |

### 5.8 数据库文档 / 监控

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/infra/db-doc/tables` | `infra:db-doc:query`（平台） |
| GET | `/infra/db-doc/table/:name` | `infra:db-doc:query`（平台） |
| GET | `/infra/db-doc/export` | `infra:db-doc:query`（平台） |
| GET | `/infra/monitor/overview` | `infra:monitor:query`（平台） |
| GET | `/infra/monitor/requests` | `infra:monitor:query`（平台） |

## 6. 前端页面

| 页面 | 路径 | 说明 |
|---|---|---|
| 租户套餐 | `system/tenant-package/index` | 列表 + 菜单树选择 + 配额输入 |
| 租户管理 | `system/tenant/index` | 表单增加套餐选择；列表展示套餐与用量 |
| 社交登录 | `system/social/index` | 客户端 CRUD + 绑定用户列表 |
| 敏感词 | `system/sensitive-word/index` | CRUD + 检测文本框 |
| 地区管理 | `system/area/index` | 只读树 + 平台导入 |
| 短信管理 | `message/sms/index` | 渠道 / 模板两个 tab |
| 邮件管理 | `message/mail/index` | 账号 / 模板两个 tab |
| 发送日志 | `message/log/index` | 列表 + 重发 |
| 访问日志 | `infra/api-log/index` | 列表 + 清空 |
| 数据库文档 | `infra/db-doc/index` | 表列表 + 列详情 + 导出 |
| 监控面板 | `infra/monitor/index` | 卡片 + 小时趋势 + 30s 刷新 |
| 登录页 | `views/login/index.vue` | 社交登录按钮 |
| 个人中心 | `views/profile/index.vue` | 社交账号绑定/解绑 |
| 通知铃铛 | `components/NotifyBell.vue` | WS → SSE → 轮询 |

新增可复用组件 `web/src/components/AreaCascader.vue`。

## 7. 迁移号、菜单、权限码、种子

### 7.1 迁移

| 迁移 | 内容 | 表数变化 |
|---|---|---|
| 00023 | `system_tenant_package` + `system_tenant.package_id` + 存量 `role_menu` 回填 | +1 → 58 |
| 00024 | `message_channel`、`message_template`、`message_log` | +3 → 61 |
| 00025 | `system_social_client`、`system_social_user` | +2 → 63 |
| 00026 | `infra_api_log` | +1 → 64 |
| 00027 | `system_sensitive_word` | +1 → 65 |
| 00028 | `system_area` | +1 → 66 |

`EXPECTED_TABLES=66`。新租户表加入 `TenantTables`：`message_log`、`system_social_user`、`system_sensitive_word`、`infra_api_log`。

### 7.2 菜单

| ID | 父 | 名称 | 组件/权限 |
|---|---|---|---|
| 110 | 100 系统管理 | 租户套餐 | `system/tenant-package/index` / `system:tenant-package:query` |
| 111 | 100 | 社交登录 | `system/social/index` / `system:social:query` |
| 112 | 100 | 敏感词 | `system/sensitive-word/index` / `system:sensitive-word:query` |
| 113 | 100 | 地区管理 | `system/area/index` / `system:area:query` |
| 204 | 200 基础设施 | 访问日志 | `infra/api-log/index` / `infra:api-log:query` |
| 205 | 200 | 数据库文档 | `infra/db-doc/index` / `infra:db-doc:query` |
| 206 | 200 | 监控面板 | `infra/monitor/index` / `infra:monitor:query` |
| 600 | 0 | 消息中心 | 目录 |
| 601 | 600 | 短信管理 | `message/sms/index` / `message:channel:query` |
| 602 | 600 | 邮件管理 | `message/mail/index` / `message:mail:query` |
| 603 | 600 | 发送日志 | `message/log/index` / `message:log:query` |

按钮按 `菜单ID * 10 + n` 分配；实施前 grep `internal/seed/seed.go` 去重。

### 7.3 种子

- 菜单种子改为「按 ID 只插入缺失项，不更新不删除」，解决存量库看不到新菜单的问题。
- `message` 种子：一条 `log` provider 的短信渠道与邮件渠道、两条欢迎模板，仅当表为空时写入。
- 租户套餐种子：一条「标准版」套餐（全部菜单、配额 0），仅当表为空时写入。
- 地区种子：省 + 市两级，表为空时写入。
- 演示种子（`GAP_SEED_DEMO=true`）：平台级渠道/模板、租户套餐、敏感词示例、社交客户端（停用）、演示消息日志；不写入真实密钥。

## 8. 测试矩阵

|  seams | 关键场景 |
|---|---|
| 套餐 | 平台 CRUD；非平台 403；删除占用 409；菜单展开含按钮；换套餐后权限缓存失效 |
| 权限语义 | 平台超管通配；租户超管仅套餐菜单；套餐外接口 403；存量回填后租户超管可见全部 |
| 配额 | 用户数边界 409；存储边界 409；短信/邮件月度边界 409；平台租户不限 |
| 消息 | 模板参数缺失/多余 400；provider 失败落日志并返回 502；密钥脱敏；重发只允许失败行；跨租户日志 404 |
| 社交 | state 重放/过期 400；未绑定 409(1901)；绑定/解绑幂等；跨租户用户 404；mock provider 全流程 |
| 推送 | 票据一次性/过期 401；SSE 头与事件；WS 握手与心跳；跨租户隔离；慢消费者丢弃计数 |
| 访问日志 | 中间件落库；`Flush` 可见；query 脱敏；清空；保留期清理；跨租户 404 |
| 敏感词 | trie 命中/打码；停用词忽略；缓存失效；公告/跟进 400 |
| 地区/IP | 种子幂等；导入父子校验；树接口；空 resolver 不影响登录日志分页 |
| DB 文档 | 含 `system_*`/`infra_*`；导出 Markdown；非平台 403 |
| 监控 | overview 字段完整；请求聚合；非平台 403 |

## 9. 验收标准

- 五条命令全绿：`make check`、`make test-integration`、`make verify-migration`（66 表）、`make web-type-check`、`make web-build`。
- 端到端：新建套餐 → 新建租户 → 租户登录只看到套餐菜单 → 平台发邮件/短信 → 租户收到日志与配额扣减 → 铃铛在 1 秒内刷新 → 访问日志与监控有数据。
- 存量升级：00023 回填后，原租户超管仍能访问全部菜单；再绑定套餐后范围收窄。
- 文档一致：README、GLOSSARY、ADR、spec 与实现一致；已知边界明确记录。
- 无 CRM/OA 回归：五验证包含现有集成测试。

## 10. 暂缓 / 开放问题

- 自家 OAuth2 授权服务器（给第三方 App 发 token）不做。
- 社交登录自动建号/邀请注册不做。
- 短信验证码登录、短信/邮件自动重试、批量发送、附件不做。
- 多实例推送（Redis/NATS pub/sub）不做。
- Prometheus/pprof/链路追踪不做。
- 密钥加密（KMS）不做，DB 明文 + API 脱敏。
- 地区数据只内置省市；区县需导入。
- 租户套餐暂不支持按模块计费、试用期、续费。
