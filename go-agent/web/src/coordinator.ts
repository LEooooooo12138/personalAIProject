import type { Identity } from './api'
import { ApiError } from './api'

type Expected = { userId: string; csrfToken: string }
type Answer = { type: 'result'; id: number; status: number; body?: string; code?: string; lease?: boolean }
type Pending = { resolve: (answer: Answer) => void; reject: (error: unknown) => void; signal?: AbortSignal; abort?: () => void; generation: number; mutation: boolean }

const workerUrl = '/app/assets/console-auth-worker.js'
const workerName = 'family-console-auth'
const version = 2
let port: MessagePort | null = null
let connecting: Promise<MessagePort> | null = null
let sequence = 0
let generation = 0
let expectedIdentity: Expected | null = null
let invalidate: (() => void) | null = null
const pending = new Map<number, Pending>()
const sockets = new Map<number, () => void>()

const coordinatedError = () => new ApiError(0, 'coordination_unavailable')
const abortError = () => new DOMException('请求已取消', 'AbortError')

export function setExpectedIdentity(identity: Identity | null) {
  expectedIdentity = identity ? { userId: identity.user.id, csrfToken: identity.csrf_token } : null
}
export function setInvalidationHandler(handler: (() => void) | null) { invalidate = handler }
function discard(id: number, entry: Pending) {
  pending.delete(id)
  if (entry.signal && entry.abort) entry.signal.removeEventListener('abort', entry.abort)
}
function handleMessage(message: unknown, source: MessagePort) {
  if (!message || typeof message !== 'object' || !('type' in message)) return
  if (message.type === 'invalidate') {
    generation += 1
    try {
      // Commit removal of private content before acknowledging the Cookie write.
      invalidate?.()
      for (const [id, entry] of pending) if (!entry.mutation) {
        discard(id, entry); source.postMessage({ type: 'cancel', id }); entry.reject(new ApiError(401, 'identity_changed'))
      }
      for (const close of sockets.values()) close()
      if ('id' in message && typeof message.id === 'number') source.postMessage({ type: 'ack', id: message.id })
    } catch { /* No ACK: the worker must fail closed. */ }
    return
  }
  if (!('id' in message) || typeof message.id !== 'number') return
  if (message.type === 'close_socket') {
    const close = sockets.get(message.id)
    if (close) close()
    else source.postMessage({ type: 'release', id: message.id }) // No socket was constructed.
    return
  }
  if (message.type !== 'result') return
  const entry = pending.get(message.id)
  if (!entry) {
    if ('lease' in message && message.lease) source.postMessage({ type: 'release', id: message.id })
    return
  }
  discard(message.id, entry)
  if (entry.generation !== generation) {
    if ('lease' in message && message.lease) source.postMessage({ type: 'release', id: message.id })
    entry.reject(new ApiError(401, 'identity_changed'))
  } else entry.resolve(message as Answer)
}
async function connect(): Promise<MessagePort> {
  if (port) return port
  if (connecting) return connecting
  connecting = new Promise<MessagePort>((resolve, reject) => {
    if (typeof SharedWorker === 'undefined') { reject(new ApiError(0, 'coordination_unsupported')); return }
    let worker: SharedWorker
    try { worker = new SharedWorker(workerUrl, { name: workerName }) }
    catch { reject(coordinatedError()); return }
    const candidate = worker.port
    const timer = window.setTimeout(() => { candidate.close(); reject(coordinatedError()) }, 5000)
    candidate.onmessage = (event: MessageEvent) => {
      if (event.data?.type === 'ready') {
        window.clearTimeout(timer)
        if (event.data.version !== version) { candidate.close(); reject(new ApiError(0, 'coordination_version_mismatch')); return }
        if (!Number.isSafeInteger(event.data.generation)) { candidate.close(); reject(coordinatedError()); return }
        generation = event.data.generation; port = candidate; resolve(candidate)
        return
      }
      handleMessage(event.data, candidate)
    }
    candidate.onmessageerror = () => {
      window.clearTimeout(timer); port = null
      for (const [id, entry] of pending) { discard(id, entry); entry.reject(coordinatedError()) }
      for (const close of sockets.values()) close()
      reject(coordinatedError())
    }
    candidate.start(); candidate.postMessage({ type: 'hello', version })
    window.addEventListener('pagehide', () => {
      for (const close of sockets.values()) close()
      candidate.postMessage({ type: 'bye' }); candidate.close()
      if (port === candidate) port = null
      for (const [id, entry] of pending) { discard(id, entry); entry.reject(abortError()) }
    }, { once: true })
  }).finally(() => { connecting = null })
  return connecting
}
async function ask(message: Record<string, unknown>, signal?: AbortSignal): Promise<Answer> {
  if (signal?.aborted) throw abortError()
  const activePort = await connect()
  if (signal?.aborted) throw abortError()
  const id = ++sequence
  return new Promise<Answer>((resolve, reject) => {
    const abort = () => {
      const entry = pending.get(id)
      if (entry) discard(id, entry)
      activePort.postMessage({ type: 'cancel', id }); reject(abortError())
    }
    pending.set(id, { resolve, reject, signal, abort, generation, mutation: message.mutation === true })
    signal?.addEventListener('abort', abort, { once: true })
    activePort.postMessage({ ...message, id, generation })
  })
}
export async function coordinatedFetch(url: string, options: RequestInit, mutation: boolean, expected?: Expected | null): Promise<Response> {
  const publicIdentity = /\/auth\/(status|me)$/.test(url)
  const identity = publicIdentity ? null : expected === undefined ? expectedIdentity : expected
  if (url.startsWith('/api/console/v1/') && !url.match(/\/auth\/(status|me|login)$/) && !identity) throw new ApiError(401, 'identity_changed')
  const answer = await ask({
    type: 'request', url, method: options.method ?? 'GET', headers: Object.fromEntries(new Headers(options.headers).entries()), body: options.body,
    mutation, expected: identity,
  }, options.signal ?? undefined)
  if (answer.code) throw new ApiError(answer.status, answer.code)
  return new Response(answer.status === 204 ? null : (answer.body ?? ''), { status: answer.status })
}
export async function openChatSocket(identity: Identity, signal: AbortSignal): Promise<WebSocket> {
  const expected: Expected = { userId: identity.user.id, csrfToken: identity.csrf_token }
  const answer = await ask({ type: 'socket', expected }, signal)
  if (answer.code || !answer.lease) throw new ApiError(answer.status, answer.code ?? 'coordination_unavailable')
  const leaseId = answer.id
  const leasePort = port!
  const release = () => { sockets.delete(leaseId); leasePort.postMessage({ type: 'release', id: leaseId }) }
  if (signal.aborted) { release(); throw abortError() }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return new Promise<WebSocket>((resolve, reject) => {
    let socket: WebSocket
    try { socket = new WebSocket(`${protocol}//${window.location.host}/api/console/v1/chat/ws`) }
    catch { release(); reject(new ApiError(0, 'network')); return }
    let closing = false
    let settled = false
    let closeTimer: number | undefined
    const settleFailure = (cause: unknown) => { if (!settled) { settled = true; reject(cause) } }
    const cleanup = () => {
      window.clearTimeout(handshakeTimer); window.clearTimeout(closeTimer)
      signal.removeEventListener('abort', abort)
      socket.removeEventListener('open', opened); socket.removeEventListener('error', failed); socket.removeEventListener('close', closed)
    }
    const close = (cause: unknown) => {
      if (closing) return
      closing = true; window.clearTimeout(handshakeTimer)
      // CONNECTING may still open after close(); only a close event proves closure.
      closeTimer = window.setTimeout(() => {
        leasePort.postMessage({ type: 'socket_failed', id: leaseId })
        settleFailure(coordinatedError())
      }, 5000)
      try { socket.close() } catch { /* Keep lease until close proof or fail closed. */ }
      settleFailure(cause)
    }
    const abort = () => close(abortError())
    const failed = () => close(new ApiError(0, 'network'))
    const closed = () => { cleanup(); release(); settleFailure(new ApiError(0, 'network')) }
    const opened = () => {
      if (closing || signal.aborted) { try { socket.close() } catch { /* Keep waiting for close. */ }; return }
      cleanup(); release(); settled = true; resolve(socket)
    }
    const handshakeTimer = window.setTimeout(() => close(new ApiError(0, 'request_timeout')), 10000)
    sockets.set(leaseId, () => close(new ApiError(401, 'identity_changed')))
    signal.addEventListener('abort', abort, { once: true })
    socket.addEventListener('open', opened); socket.addEventListener('error', failed); socket.addEventListener('close', closed)
    if (signal.aborted) abort()
  })
}
