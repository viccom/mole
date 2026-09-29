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

export function requestAuthCode(corpId: string): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!window.dd) {
      reject(new Error('DingTalk JSAPI not available'))
      return
    }

    let settled = false
    const ok = (code: string) => { if (!settled) { settled = true; resolve(code) } }
    const fail = (err: unknown) => { if (!settled) { settled = true; reject(err) } }

    window.dd.ready(() => {
      const params = {
        corpId,
        onSuccess: (res: { code: string }) => ok(res.code),
        onFail: (err: unknown) => fail(new Error(`DingTalk requestAuthCode failed: ${JSON.stringify(err)}`)),
      }
      if (window.dd?.requestAuthCode) {
        window.dd.requestAuthCode(params)
      } else if (window.dd?.runtime?.permission?.requestAuthCode) {
        window.dd.runtime.permission.requestAuthCode(params)
      } else {
        fail(new Error('DingTalk JSAPI requestAuthCode unavailable'))
      }
    })
  })
}
