declare global {
  interface Window {
    dd?: {
      ready: (callback: () => void) => void
      error: (callback: (err: unknown) => void) => void
      requestAuthCode: (params: {
        corpId: string
        onSuccess: (result: { code: string }) => void
        onFail: (err: unknown) => void
      }) => void
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
      if (!window.dd?.requestAuthCode) {
        fail(new Error('DingTalk dd.requestAuthCode not available'))
        return
      }
      window.dd.requestAuthCode({
        corpId,
        onSuccess: (res) => ok(res.code),
        onFail: (err) => fail(new Error(`DingTalk requestAuthCode failed: ${JSON.stringify(err)}`)),
      })
    })
  })
}
