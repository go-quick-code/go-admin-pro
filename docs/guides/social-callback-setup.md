# 社交登录回调地址配置指南（微信开放平台 / 钉钉 / 企业微信）

对应 issue #77，规则见 `docs/platform-mvp-spec.md` §4.4 与 ADR 0011。本文说明三家平台的
开放平台后台配置，以及系统内「社交客户端」的填写示例。

## 回调链路

```
浏览器                    go-admin-pro                    三方平台
  │ 登录页点「微信登录」      │                                │
  │──GET /system/social/authorize-url──▶│                    │
  │◀──{url, state}──────────│                                │
  │──整页跳转 url──────────────────────────────────────────▶│ 用户扫码确认
  │◀──────────重定向 redirect_uri?code=xxx&state=yyy────────│
  │（前端回调页 /social/callback）                            │
  │──GET /system/social/callback?provider&code&state──▶│     │
  │                        │──code 换 token、拉用户信息──▶│
  │◀──{mode: login, accessToken} 或 409/业务码 1901────────│
```

- **redirect_uri 指向前端回调页** `<前端域名>/social/callback`（路由公开，见
  `web/src/router/index.ts`），由回调页调用后端公开端点 `GET /system/social/callback`
  完成换登录态/绑定。
- 后端生成一次性 `state`（32 字节随机，服务端保存 10 分钟），回调校验通过后才换 token；
  钉钉额外携带 PKCE `code_challenge`（S256）。
- 未绑定的三方账号回调返回 HTTP 409 + 业务码 `1901`，前端提示先账号密码登录后到
  「个人中心 → 社交账号」绑定，**不自动建号**（ADR 0011）。

## 系统内「社交客户端」配置（平台租户管理端）

`系统管理 → 社交客户端`（或 `POST /system/social/client`）：

| 字段 | 说明 |
|---|---|
| provider | `wechat_open` / `dingtalk` / `wecom` |
| clientId | 三方平台的 AppID / ClientID / CorpID |
| clientSecret | 三方平台的 AppSecret / ClientSecret / 应用 Secret（查询脱敏） |
| redirectUri | 必须与三方后台登记的回调域名/地址一致，如 `https://admin.example.com/social/callback` |
| scopes | 留空用默认值（微信 `snsapi_login`、钉钉 `openid`；企微无 scope） |
| authorizeUrl / tokenUrl / userinfoUrl | 留空用内置默认端点；配置了自定义端点时以配置为准 |

内置默认端点（spec §4.4.8）：

| provider | 授权 | token | 用户信息 |
|---|---|---|---|
| wechat_open | `https://open.weixin.qq.com/connect/qrconnect` | `https://api.weixin.qq.com/sns/oauth2/access_token` | `https://api.weixin.qq.com/sns/userinfo` |
| dingtalk | `https://login.dingtalk.com/oauth2/auth` | `https://api.dingtalk.com/v1.0/oauth2/userAccessToken` | `https://api.dingtalk.com/v1.0/contact/users/me` |
| wecom | `https://login.work.weixin.qq.com/wwlogin/sso/login` | `https://qyapi.weixin.qq.com/cgi-bin/gettoken` | `https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo` |

企微补充：`user/get`（资料补齐）无独立配置列，端点取 `userinfo_url` 覆盖地址的
scheme+host 拼固定路径 `/cgi-bin/user/get`。

## 微信开放平台（扫码登录）

1. 开放平台（`open.weixin.qq.com`）创建「网站应用」，获得 AppID/AppSecret。
2. 应用详情 →「授权回调域」登记前端域名，如 `admin.example.com`（不带协议与路径）。
3. 系统内客户端：provider=`wechat_open`，clientId=AppID，clientSecret=AppSecret，
   redirectUri=`https://admin.example.com/social/callback`。
4. 授权地址由系统拼装：`https://open.weixin.qq.com/connect/qrconnect?appid=…&redirect_uri=…&response_type=code&scope=snsapi_login&state=…#wechat_redirect`。

说明：微信 qrconnect 不支持 PKCE，`code` 的防劫持依赖服务端一次性 `state` 与回调域配置。

## 钉钉（扫码/授权登录）

1. 钉钉开放平台（`open.dingtalk.com`）创建企业内部应用（「扫码/授权登录」方式），
   获得 Client ID（AppKey）与 Client Secret（AppKeySecret）。
2. 应用 →「登录与分享」→「重定向 URL」登记 `https://admin.example.com/social/callback`。
   需要用户基本信息权限点（Contact.User.Read）。
3. 系统内客户端：provider=`dingtalk`，clientId=Client ID，clientSecret=Client Secret，
   scopes 填 `openid`（可按需追加，空格分隔），redirectUri 同上。
4. 授权地址由系统拼装并携带 PKCE（钉钉新版 OAuth2 支持）：
   `https://login.dingtalk.com/oauth2/auth?redirect_uri=…&response_type=code&client_id=…&scope=openid&state=…&code_challenge=…&code_challenge_method=S256`。

## 企业微信（扫码登录）

1. 企业微信管理后台（`work.weixin.qq.com`）→「我的企业」拿到 CorpID；「应用管理」里
   自建应用拿到 AgentId 与 Secret。
2. 自建应用 →「网页授权及 JS-SDK」→ 设置可信域名（如 `admin.example.com`）；按提示
   完成域名归属验证。
3. 系统内客户端：provider=`wecom`，clientId=CorpID，clientSecret=应用 Secret，
   redirectUri=`https://admin.example.com/social/callback`。
4. AgentId 建议经**自定义授权地址**的 query 携带（新组件 CorpApp 场景建议填写、
   旧版 qrConnect 必填），系统合并标准参数时保留已有 query：

   ```
   https://login.work.weixin.qq.com/wwlogin/sso/login?agentid=1000002
   ```

   填入客户端的 authorizeUrl 字段即可；拼出的授权链接为
   `…wwlogin/sso/login?agentid=1000002&login_type=CorpApp&appid=…&redirect_uri=…&state=…`。

说明：企微身份键为成员 `userid`（企业内唯一），无 unionid；`gettoken` 换取的应用
access_token 只在交换过程中使用，**不落库**（spec §4.4.9：不存三方 access token，
`raw_info` 截断 4096 字节）。

## 端到端验证清单

1. 平台租户管理员配置并启用客户端；登录页出现对应平台按钮（`GET /system/social/providers`）。
2. 点击按钮跳转三方扫码页；确认后回到 `/social/callback`。
3. 已绑定 → 提示登录成功并进入首页（`system_login_log` 记录 `login_type=3`）。
4. 未绑定 → 提示先登录后绑定；账号密码登录后在「个人中心 → 社交账号」绑定，重复绑定幂等。
