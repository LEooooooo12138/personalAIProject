import { useEffect, useState } from 'react'
import { ApiError, requestConsole } from './api'
import { useAuth } from './auth'

interface QueryState<T> { key: string; epoch: number; data: T | null; error: ApiError | null; loading: boolean }
export function useFamilyQuery<T>(path: string, enabled = true) {
  const auth = useAuth()
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<QueryState<T>>({ key: path, epoch: auth.epoch, data: null, error: null, loading: true })
  useEffect(() => {
    if (!enabled) return
    let active = true
    let pending: AbortController | null = null
    let timer: ReturnType<typeof setInterval> | null = null
    const expected = auth.epoch
    setState((previous) => previous.key === path && previous.epoch === expected ? { ...previous, loading: true } : { key: path, epoch: expected, data: null, error: null, loading: true })
    const clearTimer = () => { if (timer !== null) { clearInterval(timer); timer = null } }
    const read = async () => {
      if (!active || auth.signal.aborted || document.visibilityState === 'hidden') return
      pending?.abort()
      const controller = new AbortController()
      pending = controller
      setState((previous) => ({ ...previous, loading: true }))
      try {
        const data = await requestConsole<T>(path, { signal: controller.signal })
        if (active && !controller.signal.aborted && auth.isCurrent(expected)) setState({ key: path, epoch: expected, data, error: null, loading: false })
      } catch (cause) {
        if (!active || controller.signal.aborted || !auth.isCurrent(expected)) return
        if (cause instanceof ApiError && cause.status === 401) { auth.expire(); return }
        const error = cause instanceof ApiError ? cause : new ApiError(0, 'network')
        setState((previous) => ({ ...previous, error, loading: false }))
      }
    }
    const resume = () => {
      clearTimer()
      if (document.visibilityState === 'hidden') { pending?.abort(); return }
      void read()
      timer = setInterval(() => void read(), 30000)
    }
    const abort = () => { active = false; pending?.abort(); clearTimer() }
    auth.signal.addEventListener('abort', abort, { once: true })
    document.addEventListener('visibilitychange', resume)
    resume()
    return () => { abort(); auth.signal.removeEventListener('abort', abort); document.removeEventListener('visibilitychange', resume) }
  }, [path, enabled, auth.epoch, attempt])
  const current = enabled && state.key === path && state.epoch === auth.epoch
  return { data: current ? state.data : null, error: current ? state.error : null, loading: enabled && (!current || state.loading), reload: () => setAttempt((value) => value + 1) }
}
