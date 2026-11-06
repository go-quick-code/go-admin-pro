/**
 * 社交登录流程的前端上下文（issue #77）。
 *
 * 三方回调只回传 code/state，provider 由发起方（登录页 / 个人中心绑定）在
 * sessionStorage 留底，回调页取回后再调 GET /system/social/callback。
 * 用 sessionStorage 而非 localStorage：流程与当前标签页绑定，关闭即清理。
 */
const SOCIAL_PROVIDER_KEY = 'go-admin-pro-social-provider'
const SOCIAL_REDIRECT_KEY = 'go-admin-pro-social-redirect'

/** 平台编码 → 按钮文案；未知平台回退客户端名称（由调用方兜底）。 */
export const SOCIAL_PROVIDER_LABELS: Record<string, string> = {
  wechat_open: '微信',
  dingtalk: '钉钉',
  wecom: '企业微信',
}

export function socialProviderLabel(provider: string, fallback = ''): string {
  return SOCIAL_PROVIDER_LABELS[provider] || fallback || provider
}

/** 发起社交授权前留底：回调页需要 provider；登录模式额外留底目标跳转地址。 */
export function rememberSocialFlow(provider: string, redirect?: string) {
  sessionStorage.setItem(SOCIAL_PROVIDER_KEY, provider)
  if (redirect) {
    sessionStorage.setItem(SOCIAL_REDIRECT_KEY, redirect)
  } else {
    sessionStorage.removeItem(SOCIAL_REDIRECT_KEY)
  }
}

/** 回调页取回发起方留底的 provider。 */
export function recallSocialProvider(): string {
  return sessionStorage.getItem(SOCIAL_PROVIDER_KEY) || ''
}

/** 登录模式回调成功后的跳转目标（登录页进入时的 redirect 参数）。 */
export function recallSocialRedirect(): string {
  return sessionStorage.getItem(SOCIAL_REDIRECT_KEY) || ''
}

/** 回调消费后清理留底，避免残留影响下一次流程判断。 */
export function clearSocialFlow() {
  sessionStorage.removeItem(SOCIAL_PROVIDER_KEY)
  sessionStorage.removeItem(SOCIAL_REDIRECT_KEY)
}
