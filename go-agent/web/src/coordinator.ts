import type { Identity } from './api'
import { ApiError } from './api'

type Expected = { userId: string; csrfToken: string }
type Answer = { type: 'result'; id: number; status: number; body?: string; code?: string; lease?: boolean }
type Pending = { resolve: (answer: Answer) => void; reject: (error: unknown) => void; signal?: AbortSignal; abort?: () => void }

const workerUrl = '/app/assets/console-auth-worker.js'
const workerName = 'family-console-auth'
const version = 1
let port: MessagePort | null = null
let connecting: Promise<MessagePort> | null = null
let sequence = 0
let generation = 0
let expectedIdentity: Expected | null = null
let invalidate: (() => void) | null = null
const pending = new Map<number, Pending>()

const coordinatedError = () => new ApiError(0, 'coordination_unavailable')
const abortError = () => new DOMException('请求已取消', 'AbortError')

export function setExpectedIdentity(identity: Identity | null) {
  expectedIdentity = identity ? { userId: identity.user.id, csrfToken: identity.csrf_token } : null
}

export function setInvalidationHandler(handler: (() => void) | null) { invalidate = handler }

function handleMessage(message: unknown, source: MessagePort) {
  if (!message || typeof message !== 'object' || !('type' in message)) return
  if (message.type === 'invalidate') {
    generation += 1
    try {
      // The provider commits removal of private content before acknowledging.
      invalidate?.()
      if ('id' in message && typeof message.id === 'number') source.postMessage({ type: 'ack', id: message.id })
    } catch { /* No ACK: the worker must fail closed before writing a Cookie. */ }
    return
  }
  if (message.type !== 'result' || !('id' in message) || typeof message.id !== 'number') return
  const entry = pending.get(message.id)
  if (!entry) return
  pending.delete(message.id)
  if (entry.signal && entry.abort) entry.signal.removeEventListener('abort', entry.abort)
  entry.resolve(message as Answer)
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
        if (event.data.version !== version || !Number.isSafeInteger(event.data.generation)) { candidate.close(); reject(coordinatedError()); return }
        generation = event.data.generation
        port = candidate
        resolve(candidate)
        return
      }
      handleMessage(event.data, candidate)
    }
    candidate.onmessageerror = () => {
      window.clearTimeout(timer)
      port = null
      for (const entry of pending.values()) entry.reject(coordinatedError())
      pending.clear()
      reject(coordinatedError())
    }
    candidate.start()
    candidate.postMessage({ type: 'hello', version })
    window.addEventListener('pagehide', () => {
      candidate.postMessage({ type: 'bye' })
      candidate.close()
      if (port === candidate) port = null
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
    const abort = () => { pending.delete(id); activePort.postMessage({ type: 'cancel', id }); reject(abortError()) }
    pending.set(id, { resolve, reject, signal, abort })
    signal?.addEventListener('abort', abort, { once: true })
    activePort.postMessage({ ...message, id, generation })
  })
}

export async function coordinatedFetch(url: string, options: RequestInit, mutation: boolean, expected?: Expected | null): Promise<Response> {
  const identity = expected === undefined ? expectedIdentity : expected
  if (url.startsWith('/api/console/v1/') && !url.match(/\/auth\/(status|me|login)$/) && !identity) throw new ApiError(401, 'identity_changed')
  const answer = await ask({
    type: 'request', url, method: options.method ?? 'GET', headers: options.headers ?? {}, body: options.body,
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
  const release = () => port?.postMessage({ type: 'release', id: leaseId })
  if (signal.aborted) { release(); throw abortError() }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return new Promise<WebSocket>((resolve, reject) => {
    let socket: WebSocket
    try { socket = new WebSocket(`${protocol}//${window.location.host}/api/console/v1/chat/ws`) }
    catch { release(); reject(new ApiError(0, 'network')); return }
    const fail = () => { cleanup(); socket.close(); release(); reject(new ApiError(0, 'network')) }
    const abort = () => { cleanup(); socket.close(); release(); reject(abortError()) }
    const opened = () => { cleanup(); release(); resolve(socket) }
    const cleanup = () => {
      signal.removeEventListener('abort', abort)
      socket.removeEventListener('open', opened)
      socket.removeEventListener('error', fail)
      socket.removeEventListener('close', fail)
    }
    signal.addEventListener('abort', abort, { once: true })
    socket.addEventListener('open', opened, { once: true })
    socket.addEventListener('error', fail, { once: true })
    socket.addEventListener('close', fail, { once: true })
  })
}
