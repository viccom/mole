import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useRequest } from './useRequest'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('useRequest', () => {
  it('loads immediately and stores successful data', async () => {
    const { result } = renderHook(() => useRequest(async () => 'loaded'))

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.data).toBe('loaded')
    expect(result.current.error).toBeNull()
  })

  it('keeps only the latest request result', async () => {
    const first = deferred<string>()
    const second = deferred<string>()
    let callCount = 0

    const { result } = renderHook(() => useRequest(async () => {
      callCount += 1
      return callCount === 1 ? first.promise : second.promise
    }, { immediate: false }))

    void act(() => {
      void result.current.run()
      void result.current.run()
    })

    second.resolve('second')
    await waitFor(() => {
      expect(result.current.data).toBe('second')
    })

    first.resolve('first')
    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.data).toBe('second')
  })
})
