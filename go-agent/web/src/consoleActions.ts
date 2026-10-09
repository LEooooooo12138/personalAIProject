import { useEffect, useRef, useState } from 'react'
import { ApiError, errorText, requestConsole } from './api'
import { useAuth } from './auth'

// Business writes are explicit and never replayed by the browser. The worker's
// Cookie mutation flag remains reserved for login/logout/password responses.
export function useConsoleAction() {
  const auth = useAuth()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const running = useRef(false)
  const controller = useRef(new AbortController())
  useEffect(() => { controller.current = new AbortController(); const active = controller.current; return () => active.abort() }, [])
  async function run<T>(path: string, body: object, method: 'POST' | 'PUT' = 'POST', current: () => boolean = () => true): Promise<T | undefined> {
    if (running.current || !auth.identity || controller.current.signal.aborted) return undefined
    const expected = auth.epoch
    running.current = true; setPending(true); setError('')
    const signal = AbortSignal.any([auth.signal, controller.current.signal])
    try {
      const result = await requestConsole<T>(path, { method, body, csrfToken: auth.identity.csrf_token, signal })
      if (!signal.aborted && auth.isCurrent(expected) && current()) return result
    } catch (cause) {
      if (signal.aborted || !auth.isCurrent(expected) || !current()) return undefined
      if (cause instanceof ApiError && cause.status === 401) auth.expire()
      else setError(cause instanceof ApiError && (cause.status === 0 || cause.status >= 500)
        ? `${errorText(cause)} 请求结果可能尚未确认，请先核对状态，再决定是否重试。`
        : errorText(cause))
    } finally {
      running.current = false
      if (!signal.aborted && auth.isCurrent(expected)) setPending(false)
    }
    return undefined
  }
  return { run, pending, error, clearError: () => setError('') }
}