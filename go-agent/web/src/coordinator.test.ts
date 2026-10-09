import { afterEach, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import workerSource from '../public/assets/console-auth-worker.js?raw'

vi.unmock('./coordinator')

type Message = { type: string; id?: number; generation?: number; [key: string]: unknown }
class WorkerPort {
  onmessage: ((event: { data: Message }) => void) | null = null
  replies: Message[] = []
  closed = false
  private listeners = new Set<(event: { data: Message }) => void>()
  postMessage(message: Message) { this.replies.push(message) }
  start() {}
  close() { this.closed = true }
  addEventListener(_type: string, handler: (event: { data: Message }) => void) { this.listeners.add(handler) }
  removeEventListener(_type: string, handler: (event: { data: Message }) => void) { this.listeners.delete(handler) }
  send(data: Message) {
    const event = { data }
    this.onmessage?.(event)
    for (const listener of [...this.listeners]) listener(event)
  }
}
const identity = { user: { id: 'alice' }, csrf_token: 'alice-csrf' }
const expected = { userId: 'alice', csrfToken: 'alice-csrf' }
function deferredResponse() {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((done) => { resolve = done })
  return { promise, resolve }
}
function worker(fetcher: typeof fetch) {
  const connect = new Function('fetch', 'setTimeout', 'clearTimeout', `"use strict"; let onconnect; ${workerSource}; return onconnect`)(fetcher, setTimeout, clearTimeout) as (event: { ports: WorkerPort[] }) => void
  return () => {
    const port = new WorkerPort()
    connect({ ports: [port] })
    port.send({ type: 'hello', version: 1 })
    return port
  }
}
function status(port: WorkerPort, id: number) {
  port.send({ type: 'request', id, generation: 0, url: '/api/console/v1/auth/status', method: 'GET', mutation: false, expected: null })
}
function socket(port: WorkerPort, id: number) { port.send({ type: 'socket', id, generation: 0, expected }) }
async function result(port: WorkerPort, id: number) {
  await waitFor(() => expect(port.replies.find((message) => message.type === 'result' && message.id === id)).toBeDefined())
  return port.replies.find((message) => message.type === 'result' && message.id === id)!
}
afterEach(() => { vi.unstubAllGlobals(); vi.resetModules() })

it('page leaving during socket identity check cannot block another page', async () => {
  const held = deferredResponse()
  const fetcher = vi.fn((url: RequestInfo | URL) => String(url).endsWith('/auth/me') ? held.promise : Promise.resolve(Response.json({ initialized: true })))
  const connect = worker(fetcher)
  const departing = connect(), active = connect()
  socket(departing, 1)
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
  departing.send({ type: 'bye' })
  status(active, 1)
  held.resolve(Response.json(identity))
  expect((await result(active, 1)).status).toBe(200)
  expect(departing.replies.some((message) => message.lease)).toBe(false)
})

it('cancelling a queued socket skips the handshake and allows later requests', async () => {
  const held = deferredResponse()
  const fetcher = vi.fn((_url: RequestInfo | URL) => fetcher.mock.calls.length === 1 ? held.promise : Promise.resolve(Response.json({ initialized: true })))
  const connect = worker(fetcher)
  const first = connect(), second = connect()
  status(first, 1)
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
  socket(first, 2)
  first.send({ type: 'cancel', id: 2 })
  status(second, 1)
  held.resolve(Response.json({ initialized: true }))
  expect((await result(second, 1)).status).toBe(200)
  expect(fetcher.mock.calls.some(([url]) => String(url).endsWith('/auth/me'))).toBe(false)
})

it('cancelling a socket during identity check releases the queue', async () => {
  const held = deferredResponse()
  const fetcher = vi.fn((url: RequestInfo | URL) => String(url).endsWith('/auth/me') ? held.promise : Promise.resolve(Response.json({ initialized: true })))
  const connect = worker(fetcher)
  const first = connect(), second = connect()
  socket(first, 1)
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
  first.send({ type: 'cancel', id: 1 })
  status(second, 1)
  held.resolve(Response.json(identity))
  expect((await result(second, 1)).status).toBe(200)
  expect(first.replies.some((message) => message.lease)).toBe(false)
})

it.each(['cancel', 'bye'])('%s releases an active socket lease before later requests', async (type) => {
  const fetcher = vi.fn((url: RequestInfo | URL) => Promise.resolve(Response.json(String(url).endsWith('/auth/me') ? identity : { initialized: true })))
  const connect = worker(fetcher)
  const first = connect(), second = connect()
  socket(first, 1)
  expect((await result(first, 1)).lease).toBe(true)
  status(second, 1)
  first.send({ type, id: 1 })
  expect((await result(second, 1)).status).toBe(200)
})

it('a new page port adopts its worker generation after pagehide and reconnect', async () => {
  class PagePort {
    onmessage: ((event: { data: Message }) => void) | null = null
    onmessageerror: (() => void) | null = null
    start() {}
    close() {}
    postMessage(message: Message) {
      if (message.type === 'hello') queueMicrotask(() => this.receive({ type: 'ready', version: 1, generation: 0 }))
      if (message.type === 'request') queueMicrotask(() => this.receive(message.generation === 0
        ? { type: 'result', id: message.id, status: 200, body: '{"initialized":true}' }
        : { type: 'result', id: message.id, status: 0, code: 'identity_changed' }))
    }
    receive(data: Message) { this.onmessage?.({ data }) }
  }
  class Shared {
    static ports: PagePort[] = []
    port = new PagePort()
    constructor() { Shared.ports.push(this.port) }
  }
  vi.stubGlobal('SharedWorker', Shared)
  const coordinator = await import('./coordinator')
  coordinator.setInvalidationHandler(() => {})
  const first = await coordinator.coordinatedFetch('/api/console/v1/auth/status', {}, false)
  expect(first.ok).toBe(true)
  Shared.ports[0].receive({ type: 'invalidate', id: 1 })
  window.dispatchEvent(new Event('pagehide'))
  const next = await coordinator.coordinatedFetch('/api/console/v1/auth/status', {}, false)
  expect(await next.json()).toEqual({ initialized: true })
})


it('fails closed with an explicit browser support error when SharedWorker is absent', async () => {
  vi.stubGlobal('SharedWorker', undefined)
  const fetcher = vi.fn()
  vi.stubGlobal('fetch', fetcher)
  const coordinator = await import('./coordinator')
  await expect(coordinator.coordinatedFetch('/api/console/v1/auth/status', {}, false)).rejects.toMatchObject({
    code: 'coordination_unsupported',
    message: '当前浏览器不支持安全认证协调，请使用支持 SharedWorker 的浏览器。',
  })
  expect(fetcher).not.toHaveBeenCalled()
})


it('an unavailable identity check does not claim that a valid session is revoked', async () => {
  const fetcher = vi.fn((_url: RequestInfo | URL) => Promise.resolve(Response.json({ error: { code: 'unavailable' } }, { status: 503 })))
  const connect = worker(fetcher)
  const page = connect()
  page.send({ type: 'request', id: 1, generation: 0, url: '/api/console/v1/auth/logout', method: 'POST', mutation: true, expected })
  const answer = await result(page, 1)
  expect(answer).toMatchObject({ status: 503, code: 'unavailable' })
  expect(fetcher.mock.calls).toHaveLength(1)
  expect(String(fetcher.mock.calls[0][0])).toBe('/api/console/v1/auth/me')
})
