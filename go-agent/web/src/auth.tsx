import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ApiError, consoleApi, errorText, type Identity } from './api'
import { flushSync } from 'react-dom'
import { setExpectedIdentity, setInvalidationHandler } from './coordinator'

type AuthStatus = 'loading' | 'anonymous' | 'ready' | 'uninitialized' | 'unavailable' | 'logout_failed'
interface AuthContextValue {
  identity: Identity | null
  status: AuthStatus
  error: string | null
  epoch: number
  signal: AbortSignal
  isCurrent: (epoch: number) => boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  retryLogout: () => Promise<void>
  changePassword: (oldPassword: string, newPassword: string) => Promise<void>
  expire: () => void
  refresh: () => Promise<void>
}
const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<Identity | null>(null)
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [error, setError] = useState<string | null>(null)
  const [epoch, setEpoch] = useState(0)
  const epochRef = useRef(0)
  const controllerRef = useRef(new AbortController())
  const identityRef = useRef<Identity | null>(null)
  const channelRef = useRef<BroadcastChannel | null>(null)
  const cookieMutationsRef = useRef(new Set<Promise<void>>())
  const revocationRef = useRef<{ userId: string; csrfToken: string } | null>(null)

  const nextEpoch = useCallback(() => {
    controllerRef.current.abort()
    controllerRef.current = new AbortController()
    epochRef.current += 1
    setEpoch(epochRef.current)
    return epochRef.current
  }, [])
  const isCurrent = useCallback((candidate: number) => candidate === epochRef.current, [])
  const clear = useCallback((broadcast = false) => {
    nextEpoch()
    identityRef.current = null
    setExpectedIdentity(null)
    revocationRef.current = null
    setIdentity(null)
    setStatus('anonymous')
    setError(null)
    if (broadcast) channelRef.current?.postMessage('invalidate')
  }, [nextEpoch])
  const expire = useCallback(() => clear(true), [clear])

  const refresh = useCallback(async () => {
    const expected = epochRef.current
    const signal = controllerRef.current.signal
    try {
      const next = await consoleApi.me(signal)
      if (!isCurrent(expected)) return
      const previous = identityRef.current
      if (previous && (previous.user.id !== next.user.id || previous.csrf_token !== next.csrf_token || previous.user.role !== next.user.role || previous.user.must_change_password !== next.user.must_change_password || previous.capabilities.join('\0') !== next.capabilities.join('\0'))) nextEpoch()
      identityRef.current = next
      setExpectedIdentity(next)
      setIdentity(next)
      setStatus('ready')
      setError(null)
    } catch (cause) {
      if (!isCurrent(expected)) return
      if (cause instanceof ApiError && cause.status === 401) {
        if (identityRef.current) clear(true)
        else {
          try {
            const answer = await consoleApi.status(signal)
            if (!isCurrent(expected)) return
            setStatus(answer.initialized ? 'anonymous' : 'uninitialized')
            setError(null)
          } catch (statusError) {
            if (!isCurrent(expected)) return
            setStatus('unavailable')
            setError(errorText(statusError))
          }
        }
      } else if (cause instanceof ApiError && cause.code === 'not_initialized') {
        nextEpoch()
        identityRef.current = null
        setExpectedIdentity(null)
        setIdentity(null)
        setStatus('uninitialized')
        setError(null)
      } else if (!(cause instanceof DOMException && cause.name === 'AbortError')) {
        nextEpoch()
        identityRef.current = null
        setExpectedIdentity(null)
        setIdentity(null)
        setStatus('unavailable')
        setError(errorText(cause))
      }
    }
  }, [clear, isCurrent, nextEpoch])

  useEffect(() => {
    setInvalidationHandler(() => flushSync(() => clear()))
    const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('family-console-auth')
    channelRef.current = channel
    if (channel) channel.onmessage = (event) => { if (event.data === 'invalidate') clear() }
    const onFocus = () => { if (identityRef.current) void refresh() }
    const onVisible = () => { if (document.visibilityState === 'visible') onFocus() }
    window.addEventListener('focus', onFocus)
    document.addEventListener('visibilitychange', onVisible)
    void refresh()
    return () => {
      nextEpoch()
      setInvalidationHandler(null)
      setExpectedIdentity(null)
      channel?.close()
      channelRef.current = null
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [clear, nextEpoch, refresh])

  const login = useCallback(async (username: string, password: string) => {
    // An older logout or password change may clear the HttpOnly cookie in its
    // response. Wait for that response before requesting a new cookie.
    while (cookieMutationsRef.current.size > 0) await Promise.allSettled([...cookieMutationsRef.current])
    if (revocationRef.current) throw new ApiError(0, 'coordination_unavailable')
    const expected = nextEpoch()
    identityRef.current = null
    setExpectedIdentity(null)
    setIdentity(null)
    setStatus((previous) => previous === 'anonymous' ? 'anonymous' : 'loading')
    setError(null)
    try {
      const next = await consoleApi.login(username, password, controllerRef.current.signal)
      if (!isCurrent(expected)) return
      identityRef.current = next
      setExpectedIdentity(next)
      setIdentity(next)
      setStatus('ready')

    } catch (cause) {
      if (!isCurrent(expected)) return
      setStatus('anonymous')
      throw cause
    }
  }, [isCurrent, nextEpoch])

  const logout = useCallback(async () => {
    const current = identityRef.current
    clear()
    if (!current) return
    const revoked = { userId: current.user.id, csrfToken: current.csrf_token }
    revocationRef.current = revoked
    setStatus('loading')
    const expected = epochRef.current
    const pending = consoleApi.logout(revoked.csrfToken, undefined, revoked)
    cookieMutationsRef.current.add(pending)
    try {
      await pending
      if (isCurrent(expected)) { revocationRef.current = null; setStatus('anonymous') }
    } catch (cause) {
      if (isCurrent(expected)) { setStatus('logout_failed'); setError(`退出登录未完成。${errorText(cause)}`) }
    } finally {
      cookieMutationsRef.current.delete(pending)
    }
  }, [clear, isCurrent])

  const retryLogout = useCallback(async () => {
    const revoked = revocationRef.current
    if (!revoked) return
    const expected = epochRef.current
    setStatus('loading')
    try {
      const actual = await consoleApi.me()
      if (!isCurrent(expected) || revocationRef.current !== revoked) return
      if (actual.user.id !== revoked.userId || actual.csrf_token !== revoked.csrfToken) {
        revocationRef.current = null
        setStatus('anonymous')
        setError(null)
        return
      }
      await consoleApi.logout(revoked.csrfToken, undefined, revoked)
      if (isCurrent(expected)) { revocationRef.current = null; setStatus('anonymous'); setError(null) }
    } catch (cause) {
      if (!isCurrent(expected)) return
      if (cause instanceof ApiError && cause.status === 401) {
        revocationRef.current = null
        setStatus('anonymous')
        setError(null)
      } else {
        setStatus('logout_failed')
        setError('退出登录未完成。' + errorText(cause))
      }
    }
  }, [isCurrent])

  const changePassword = useCallback(async (oldPassword: string, newPassword: string) => {
    const current = identityRef.current
    if (!current) throw new ApiError(401, 'unauthenticated')
    const expectedIdentity = { userId: current.user.id, csrfToken: current.csrf_token }
    clear()
    setStatus('loading')
    const expected = epochRef.current
    // Let the response settle even if the local identity is invalidated:
    // a late Set-Cookie can otherwise race a subsequent login.
    const pending = consoleApi.changePassword(oldPassword, newPassword, expectedIdentity.csrfToken, undefined, expectedIdentity)
    cookieMutationsRef.current.add(pending)
    try {
      await pending
      if (isCurrent(expected)) setStatus('anonymous')
    } catch (cause) {
      if (isCurrent(expected)) { setStatus('unavailable'); setError(errorText(cause)) }
      throw cause
    } finally {
      cookieMutationsRef.current.delete(pending)
    }
  }, [clear, isCurrent])

  const value = useMemo<AuthContextValue>(() => ({
    identity, status, error, epoch, signal: controllerRef.current.signal, isCurrent,
    login, logout, retryLogout, changePassword, expire, refresh,
  }), [identity, status, error, epoch, isCurrent, login, logout, retryLogout, changePassword, expire, refresh])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('AuthProvider is required')
  return value
}
