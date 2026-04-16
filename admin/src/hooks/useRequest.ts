import { useCallback, useEffect, useRef, useState } from 'react'

type UseRequestOptions<T> = {
  immediate?: boolean
  onSuccess?: (data: T) => void
  onError?: (error: Error) => void
}

export function useRequest<T>(
  requestFn: () => Promise<T>,
  options: UseRequestOptions<T> = {},
) {
  const { immediate = true, onSuccess, onError } = options
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(immediate)
  const [error, setError] = useState<Error | null>(null)
  const mountedRef = useRef(true)
  const requestIdRef = useRef(0)
  const requestFnRef = useRef(requestFn)
  const onSuccessRef = useRef(onSuccess)
  const onErrorRef = useRef(onError)

  useEffect(() => {
    requestFnRef.current = requestFn
    onSuccessRef.current = onSuccess
    onErrorRef.current = onError
  }, [onError, onSuccess, requestFn])

  useEffect(() => {
    return () => {
      mountedRef.current = false
    }
  }, [])

  const run = useCallback(async () => {
    const requestId = ++requestIdRef.current
    setLoading(true)
    setError(null)

    try {
      const result = await requestFnRef.current()
      if (!mountedRef.current || requestId !== requestIdRef.current) {
        return result
      }
      setData(result)
      onSuccessRef.current?.(result)
      return result
    } catch (err: unknown) {
      const requestError = err instanceof Error ? err : new Error('Request failed')
      if (mountedRef.current && requestId === requestIdRef.current) {
        setError(requestError)
        onErrorRef.current?.(requestError)
      }
      throw requestError
    } finally {
      if (mountedRef.current && requestId === requestIdRef.current) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    if (!immediate) return
    void run().catch(() => {})
  }, [immediate, run])

  return {
    data,
    loading,
    error,
    run,
    setData,
  }
}
