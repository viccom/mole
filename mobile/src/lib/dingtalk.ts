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
