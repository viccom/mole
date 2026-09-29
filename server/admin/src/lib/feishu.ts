declare global {
  interface Window {
    h5sdk?: {
      ready: (callback: () => void) => void
      error: (callback: (err: unknown) => void) => void
    }
    tt?: {
      requestAuthCode: (params: {
        appId: string
        success: (res: { code: string }) => void
        fail: (err: unknown) => void
      }) => void
    }
  }
}

export function isFeishuEnv(): boolean {
  if (typeof window === 'undefined') return false
  const ua = navigator.userAgent.toLowerCase()
  return ua.includes('feishu') || ua.includes('lark')
}

export function buildOAuth2URL(appId: string): string {
  const base = window.location.origin + '/admin/feishu-callback'
  const params = new URLSearchParams({
    app_id: appId,
    redirect_uri: base,
    state: randomState(),
  })
  return `https://open.feishu.cn/open-apis/authen/v1/authorize?${params.toString()}`
}

function randomState(): string {
  const arr = new Uint8Array(16)
  crypto.getRandomValues(arr)
  return Array.from(arr, b => b.toString(16).padStart(2, '0')).join('')
}

export function requestAuthCode(appId: string): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!window.h5sdk) {
      reject(new Error('Feishu JSAPI not available'))
      return
    }

    let settled = false
    const ok = (code: string) => { if (!settled) { settled = true; resolve(code) } }
    const fail = (err: unknown) => { if (!settled) { settled = true; reject(err) } }

    window.h5sdk.error((err) => fail(new Error(`Feishu h5sdk error: ${JSON.stringify(err)}`)))
    window.h5sdk.ready(() => {
      if (!window.tt?.requestAuthCode) {
        fail(new Error('Feishu tt.requestAuthCode not available'))
        return
      }
      window.tt.requestAuthCode({
        appId,
        success: (res) => ok(res.code),
        fail: (err) => fail(new Error(`Feishu requestAuthCode failed: ${JSON.stringify(err)}`)),
      })
    })
  })
}
