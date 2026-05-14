declare global {
  interface Window {
    dd?: {
      ready: (callback: () => void) => void
      error: (callback: (err: unknown) => void) => void
      requestAuthCode?: (params: {
        corpId: string
        onSuccess: (result: { code: string }) => void
        onFail: (err: unknown) => void
      }) => void
      runtime?: {
        permission?: {
          requestAuthCode?: (params: {
            corpId: string
            onSuccess: (result: { code: string }) => void
            onFail: (err: unknown) => void
          }) => void
        }
      }
    }
  }
}

export function isDingTalkEnv(): boolean {
  if (typeof window === 'undefined') return false
  const ua = navigator.userAgent.toLowerCase()
  return ua.includes('dingtalk')
}

export function buildDingTalkOAuth2URL(appKey: string): string {
  const base = window.location.origin + '/admin/dingtalk-callback'
  const params = new URLSearchParams({
    client_id: appKey,
    redirect_uri: base,
    response_type: 'code',
    scope: 'openid',
    state: randomState(),
    prompt: 'consent',
  })
  return `https://login.dingtalk.com/oauth2/auth?${params.toString()}`
}

function randomState(): string {
  const arr = new Uint8Array(16)
  crypto.getRandomValues(arr)
  return Array.from(arr, b => b.toString(16).padStart(2, '0')).join('')
}

export function requestAuthCode(corpId: string): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!window.dd) {
      reject(new Error('DingTalk JSAPI not available'))
      return
    }

    let settled = false
    const ok = (code: string) => { if (!settled) { settled = true; resolve(code) } }
    const fail = (err: unknown) => { if (!settled) { settled = true; reject(err) } }

    window.dd.error((err) => fail(new Error(`DingTalk dd error: ${JSON.stringify(err)}`)))
    window.dd.ready(() => {
      const params = {
        corpId,
        onSuccess: (res: { code: string }) => ok(res.code),
        onFail: (err: unknown) => fail(new Error(`DingTalk requestAuthCode failed: ${JSON.stringify(err)}`)),
      }

      // 优先使用 dd.requestAuthCode（移动端），回退到 dd.runtime.permission.requestAuthCode（PC端）
      if (window.dd?.requestAuthCode) {
        window.dd.requestAuthCode(params)
      } else if (window.dd?.runtime?.permission?.requestAuthCode) {
        window.dd.runtime.permission.requestAuthCode(params)
      } else {
        fail(new Error('DingTalk dd.requestAuthCode not available'))
      }
    })
  })
}
