import { runInNewContext } from 'node:vm'
import { afterEach, expect, it, vi } from 'vitest'
import source from '../public/assets/console-auth-worker.js?raw'

type Message = { type: string; id?: number; [key: string]: unknown }
class Port {
  onmessage: ((event: { data: Message }) => void) | null = null
  messages: Message[] = []
  postMessage(message: Message) { this.messages.push(message) }
  start() {}
  close() {}
  send(data: Message) { this.onmessage?.({ data }) }
}
const activePorts: Port[] = []
const expected = { userId: 'alice', csrfToken: 'alice-csrf' }
const identity = { user: { id: 'alice' }, csrf_token: 'alice-csrf' }
const flush = async () => { for (let i = 0; i < 30; i++) await Promise.resolve() }
function held() {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((done) => { resolve = done })
  return { promise, resolve }
}
function harness(fetcher: typeof fetch, version = 2) {
  const context = { fetch: fetcher, setTimeout, clearTimeout, AbortController, Headers, URL, onconnect: null as unknown as (event: { ports: Port[] }) => void }
  runInNewContext(source, context)
  const connect = () => { const port = new Port(); activePorts.push(port); context.onconnect({ ports: [port] }); port.send({ type: 'hello', version }); return port }
  return { connect }
}
function request(port: Port, id: number, url: string, extra = {}) { port.send({ type: 'request', id, generation: 0, url: '/api/console/v1' + url, method: 'GET', mutation: false, expected: null, ...extra }) }
const answer = (port: Port, id: number) => port.messages.find((message) => message.type === 'result' && message.id === id)
afterEach(() => { for (const port of activePorts.splice(0)) port.send({ type: 'bye' }); vi.useRealTimers() })

it('cancelled_query_does_not_block_another_port', async () => {
  const query = held()
  const fetcher = vi.fn((url: RequestInfo | URL, _options?: RequestInit) => String(url).endsWith('/auth/status') ? Promise.resolve(Response.json({ initialized: true })) : query.promise)
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  request(first, 1, '/admin/knowledge/query', { method: 'POST' })
  await flush()
  expect(fetcher.mock.calls[0][1]?.signal).toBeDefined()
  request(second, 1, '/auth/status')
  await flush()
  expect(answer(second, 1)?.status).toBe(200)
  first.send({ type: 'cancel', id: 1 })
  expect(fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true)
  query.resolve(Response.json({ answer: 'late private answer' }))
  await flush()
  expect(answer(first, 1)).toBeUndefined()
})

it('same_id_on_two_ports_cancels_only_its_owner', async () => {
  const pending = held()
  const fetcher = vi.fn((_url: RequestInfo | URL, _options?: RequestInit) => pending.promise)
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  request(first, 7, '/sessions'); request(second, 7, '/sessions')
  await flush()
  expect(fetcher).toHaveBeenCalledTimes(2)
  first.send({ type: 'cancel', id: 7 })
  expect(fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true)
  expect(fetcher.mock.calls[1][1]?.signal?.aborted).toBe(false)
  second.send({ type: 'bye' })
  expect(fetcher.mock.calls[1][1]?.signal?.aborted).toBe(true)
  pending.resolve(Response.json({ sessions: [] }))
  await flush()
  expect(answer(first, 7)).toBeUndefined(); expect(answer(second, 7)).toBeUndefined()
})

it('identity_precheck_is_cancellable_and_does_not_dispatch_business', async () => {
  const pending = held()
  const fetcher = vi.fn((_url: RequestInfo | URL, _options?: RequestInit) => pending.promise)
  const { connect } = harness(fetcher)
  const port = connect()
  request(port, 1, '/sessions', { expected })
  await flush()
  port.send({ type: 'cancel', id: 1 })
  expect(fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true)
  pending.resolve(Response.json(identity)); await flush()
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(answer(port, 1)).toBeUndefined()
})


function acknowledge(port: Port) {
  for (const notice of port.messages.filter((message) => message.type === 'invalidate')) port.send({ type: 'ack', id: notice.id })
}
function mutation(port: Port, id: number, url = '/auth/login', generation = 0) {
  request(port, id, url, { method: 'POST', mutation: true, generation })
}

it('late_cookie_blocks_new_login_even_after_cancel_and_ten_seconds', async () => {
  vi.useFakeTimers()
  const oldCookie = held()
  const fetcher = vi.fn((url: RequestInfo | URL) => String(url).endsWith('/auth/logout') ? oldCookie.promise : Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  mutation(first, 1, '/auth/logout')
  await flush(); acknowledge(second); await flush()
  expect(fetcher).toHaveBeenCalledTimes(1)
  first.send({ type: 'cancel', id: 1 })
  mutation(second, 1, '/auth/login', 1)
  await vi.advanceTimersByTimeAsync(10001)
  expect(fetcher).toHaveBeenCalledTimes(1)
  oldCookie.resolve(new Response(null, { status: 204 }))
  await flush(); acknowledge(first); await flush()
  expect(fetcher.mock.calls.map(([url]) => String(url))).toEqual(['/api/console/v1/auth/logout', '/api/console/v1/auth/login'])
  expect(answer(first, 1)).toBeUndefined()
  expect(answer(second, 1)?.status).toBe(200)
})

it('ack_timeout_prevents_cookie_write', async () => {
  vi.useFakeTimers()
  const fetcher = vi.fn(() => Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const first = connect(); connect()
  mutation(first, 1)
  await flush()
  await vi.advanceTimersByTimeAsync(5000)
  expect(answer(first, 1)?.code).toBe('coordination_unavailable')
  expect(fetcher).not.toHaveBeenCalled()
})

it('connecting_socket_close_before_cookie_mutation', async () => {
  vi.useFakeTimers()
  const fetcher = vi.fn(() => Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  first.send({ type: 'socket', id: 9, generation: 0, expected })
  await flush(); expect(answer(first, 9)?.lease).toBe(true)
  mutation(second, 1)
  await flush(); acknowledge(first); await flush()
  first.send({ type: 'cancel', id: 9 })
  await flush()
  expect(first.messages.some((message) => message.type === 'close_socket' && message.id === 9)).toBe(true)
  expect(fetcher).toHaveBeenCalledTimes(1)
  first.send({ type: 'release', id: 9 }); await flush()
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(answer(second, 1)?.status).toBe(200)
})

it.each(['before', 'during'])('departing_port_cannot_prove_connecting_socket_closed: bye %s mutation', async (timing) => {
  vi.useFakeTimers()
  const fetcher = vi.fn(() => Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  first.send({ type: 'socket', id: 9, generation: 0, expected })
  await flush(); expect(answer(first, 9)?.lease).toBe(true)
  if (timing === 'during') { mutation(second, 1); await flush() }
  // Mirrors pagehide: close was requested locally, then bye closes the port.
  // No release / WebSocket close event has confirmed handshake termination.
  first.send({ type: 'bye' })
  if (timing === 'before') mutation(second, 1)
  await flush()
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(answer(second, 1)).toBeUndefined()
  await vi.advanceTimersByTimeAsync(4999)
  expect(fetcher).toHaveBeenCalledTimes(1)
  await vi.advanceTimersByTimeAsync(1)
  expect(answer(second, 1)?.code).toBe('coordination_unavailable')
  expect(fetcher).toHaveBeenCalledTimes(1)
  // A later Cookie operation cannot reopen the lane after missing proof.
  mutation(second, 2); await flush()
  expect(answer(second, 2)?.code).toBe('coordination_unavailable')
  expect(fetcher).toHaveBeenCalledTimes(1)
})
it('unproven_socket_closure_fails_the_cookie_barrier_closed', async () => {
  vi.useFakeTimers()
  const fetcher = vi.fn(() => Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  first.send({ type: 'socket', id: 1, generation: 0, expected }); await flush()
  mutation(second, 2); await flush(); acknowledge(first)
  await vi.advanceTimersByTimeAsync(5000)
  expect(answer(second, 2)?.code).toBe('coordination_unavailable')
  expect(fetcher).toHaveBeenCalledTimes(1)
  first.send({ type: 'release', id: 1 })
  mutation(second, 3); await flush()
  expect(answer(second, 3)?.code).toBe('coordination_unavailable')
  expect(fetcher).toHaveBeenCalledTimes(1)
})

it('stale_generation_response_ignored', async () => {
  const late = held()
  const fetcher = vi.fn((url: RequestInfo | URL) => String(url).endsWith('/sessions') ? late.promise : Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const first = connect(), second = connect()
  request(first, 1, '/sessions'); await flush()
  mutation(second, 1); await flush(); acknowledge(first); await flush()
  expect(answer(first, 1)?.code).toBe('identity_changed')
  late.resolve(Response.json({ secret: 'NEW-ACCOUNT-DATA' })); await flush()
  expect(first.messages.filter((message) => message.type === 'result' && message.id === 1)).toHaveLength(1)
  expect(JSON.stringify(first.messages)).not.toContain('NEW-ACCOUNT-DATA')
})

it('old_worker_version_rejected', async () => {
  const fetcher = vi.fn(() => Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher, 1)
  const old = connect(); request(old, 1, '/sessions'); await flush()
  expect(old.messages[0]).toMatchObject({ type: 'ready', version: 2 })
  expect(fetcher).not.toHaveBeenCalled()
})

it.each([
  ['/sessions', 'GET', 15000], ['/admin/members', 'POST', 60000],
  ['/admin/knowledge/query', 'POST', 310000], ['/admin/analyze', 'POST', 310000], ['/admin/wiki/ingest', 'POST', 310000],
])('ordinary_timeout_is_not_a_write_retry: %s %s', async (url, method, deadline) => {
  vi.useFakeTimers()
  const pending = held()
  const fetcher = vi.fn((_url: RequestInfo | URL, _options?: RequestInit) => pending.promise)
  const { connect } = harness(fetcher)
  const port = connect(); request(port, 1, url, { method }); await flush()
  await vi.advanceTimersByTimeAsync(Number(deadline) - 1)
  expect(answer(port, 1)).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1)
  expect(answer(port, 1)?.code).toBe('request_timeout')
  expect(fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true)
  pending.resolve(Response.json({ late: true })); await flush()
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(port.messages.filter((message) => message.type === 'result')).toHaveLength(1)
})

it('identity_check_has_its_own_ten_second_deadline', async () => {
  vi.useFakeTimers()
  const pending = held()
  const fetcher = vi.fn((_url: RequestInfo | URL, _options?: RequestInit) => pending.promise)
  const { connect } = harness(fetcher)
  const port = connect(); request(port, 1, '/sessions', { expected }); await flush()
  await vi.advanceTimersByTimeAsync(10000)
  expect(answer(port, 1)?.code).toBe('request_timeout')
  expect(fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true)
  expect(fetcher).toHaveBeenCalledTimes(1)
})

it('expected_headers_are_worker_owned_and_public_identity_reads_are_unbound', async () => {
  const fetcher = vi.fn((_url: RequestInfo | URL, _options?: RequestInit) => Promise.resolve(Response.json(identity)))
  const { connect } = harness(fetcher)
  const port = connect()
  request(port, 1, '/sessions', { expected, headers: { 'X-Console-Expected-User': 'forged', 'X-CSRF-Token': 'forged' } }); await flush()
  const headers = fetcher.mock.calls[1][1]?.headers as Headers
  expect(headers.get('X-Console-Expected-User')).toBe(expected.userId)
  expect(headers.get('X-CSRF-Token')).toBe(expected.csrfToken)
  request(port, 2, '/auth/me', { expected }); await flush()
  expect(fetcher).toHaveBeenCalledTimes(3)
  expect((fetcher.mock.calls[2][1]?.headers as Headers).has('X-Console-Expected-User')).toBe(false)
})
