import http, { type IncomingHttpHeaders } from 'node:http'
import net from 'node:net'

export type GateMode = 'response' | 'body' | 'fail' | 'request'
export type Gate = { seen: Promise<void>; completed: Promise<void>; status: Promise<number>; replaceCookie(cookie: string): void; release(): void }
type InternalGate = Gate & { mode: GateMode; used: boolean; held: Promise<void>; markSeen(): void; markDone(): void; markStatus(status: number): void; cookie?: string }

// Transport faults live only in browser tests; the application has no test endpoint.
export async function startAuthProxy(upstreamOrigin: string, presentationFixtures: ReadonlyMap<string, unknown> = new Map()) {
  const upstream = new URL(upstreamOrigin)
  if (upstream.protocol !== 'http:' || !['127.0.0.1', '[::1]'].includes(upstream.hostname)) throw new Error('test upstream must be HTTP loopback')
  const gates = new Map<string, InternalGate>()
  const counts = new Map<string, number>()
  const sockets = new Set<net.Socket>()
  let origin = ''
  function forwardedHeaders(headers: IncomingHttpHeaders) {
    const next = { ...headers, host: upstream.host }
    if (next.origin === origin) next.origin = upstream.origin
    return next
  }
  const server = http.createServer((request, response) => {
    if (!request.url?.startsWith('/') || request.url.startsWith('//')) { response.writeHead(400); response.end(); return }
    const path = new URL(request.url ?? '/', origin).pathname
    counts.set(path, (counts.get(path) ?? 0) + 1)
    // Optional read-only exceptional DTOs are isolated presentation fixtures.
    if (request.method === 'GET' && presentationFixtures.has(path)) {
      request.resume(); response.writeHead(200, { 'Content-Type': 'application/json' }); response.end(JSON.stringify(presentationFixtures.get(path))); return
    }
    const waiting = gates.get(path)
    const gate = waiting && !waiting.used ? waiting : undefined
    if (gate) gate.used = true
    if (gate?.mode === 'fail') {
      request.resume()
      gate.markSeen()
      response.writeHead(503, { 'Content-Type': 'application/json' })
      gate.markStatus(503)
      response.end(JSON.stringify({ error: { code: 'unavailable', request_id: 'proxy-fixture' } }))
      gate.markDone()
      return
    }
    const forward = (body?: Buffer) => {
    const headers = forwardedHeaders(request.headers)
    if (gate?.cookie) headers.cookie = gate.cookie
    const outgoing = http.request(new URL(request.url ?? '/', upstream), {
      method: request.method, headers,
    }, (incoming) => {
      const chunks: Buffer[] = []
      incoming.on('data', (chunk: Buffer) => chunks.push(chunk))
      incoming.on('error', () => { response.destroy(); gate?.markDone() })
      incoming.on('end', () => {
        const headers = { ...incoming.headers }
        const csp = headers['content-security-policy']
        if (typeof csp === 'string') headers['content-security-policy'] = csp.replaceAll(`ws://${upstream.host}`, `ws://${new URL(origin).host}`)
        const status = incoming.statusCode ?? 502
        gate?.markStatus(status)
        void (async () => {
          if (gate?.mode === 'body') {
            response.writeHead(status, headers)
            response.flushHeaders()
          }
          gate?.markSeen()
          if (gate) await gate.held
          if (!response.destroyed) {
            if (!response.headersSent) response.writeHead(status, headers)
            response.end(Buffer.concat(chunks))
          }
          gate?.markDone()
        })()
      })
    })
    outgoing.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end(); gate?.markDone() })
    if (body) outgoing.end(body)
    else request.pipe(outgoing)
    }
    if (gate?.mode === 'request') {
      const body: Buffer[] = []
      request.on('data', (chunk: Buffer) => body.push(chunk))
      request.on('end', () => { gate.markSeen(); void gate.held.then(() => forward(Buffer.concat(body))) })
    } else forward()
  })
  server.on('connection', (socket) => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)) })
  server.on('upgrade', (request, socket, head) => {
    if (!request.url?.startsWith('/') || request.url.startsWith('//')) { socket.destroy(); return }
    const outgoing = http.request(new URL(request.url ?? '/', upstream), { headers: forwardedHeaders(request.headers) })
    outgoing.on('upgrade', (incoming, upstreamSocket, upstreamHead) => {
      const headers = Object.entries(incoming.headers).flatMap(([key, value]) => Array.isArray(value) ? value.map((item) => `${key}: ${item}`) : value === undefined ? [] : [`${key}: ${value}`])
      socket.write(`HTTP/1.1 ${incoming.statusCode} ${incoming.statusMessage}\r\n${headers.join('\r\n')}\r\n\r\n`)
      if (head.length) upstreamSocket.write(head)
      if (upstreamHead.length) socket.write(upstreamHead)
      socket.pipe(upstreamSocket).pipe(socket)
      socket.on('close', () => upstreamSocket.destroy())
      upstreamSocket.on('error', () => socket.destroy())
    })
    outgoing.on('response', (incoming) => { socket.end(`HTTP/1.1 ${incoming.statusCode} Rejected\r\nConnection: close\r\n\r\n`); incoming.resume() })
    outgoing.on('error', () => socket.destroy())
    socket.on('error', () => outgoing.destroy())
    outgoing.end()
  })
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('proxy did not bind loopback')
  origin = `http://127.0.0.1:${address.port}`
  return {
    origin,
    counts: (path: string) => counts.get(path) ?? 0,
    gate(path: string, mode: GateMode): Gate {
      let markSeen!: () => void, markDone!: () => void, release!: () => void, markStatus!: (status: number) => void
      const seen = new Promise<void>((resolve) => { markSeen = resolve })
      const completed = new Promise<void>((resolve) => { markDone = resolve })
      const status = new Promise<number>((resolve) => { markStatus = resolve })
      const held = new Promise<void>((resolve) => { release = resolve })
      const gate: InternalGate = { mode, used: false, seen, completed, status, held, release, markSeen, markDone, markStatus, replaceCookie(cookie: string) { gate.cookie = cookie } }
      gates.set(path, gate)
      return gate
    },
    async close() {
      for (const gate of gates.values()) gate.release()
      for (const socket of sockets) socket.destroy()
      await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()))
    },
  }
}
