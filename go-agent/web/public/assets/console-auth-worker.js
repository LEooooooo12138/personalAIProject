// The URL and worker name must remain stable across frontend versions.
const VERSION = 2
const ports = new Set()
const generations = new Map()
const acknowledgements = new Map()
// IDs belong to a page port, never to the entire worker.
const requests = new Map()
const leases = new Map()
let cookieTail = Promise.resolve()
let cookiePending = 0
let coordinationFailed = false
let noticeId = 0

function reply(port, id, result) {
  if (!ports.has(port)) return
  try { port.postMessage({ type: 'result', id, ...result }) } catch { /* Fail closed if the page vanished. */ }
}
function publicIdentityURL(url) { return /\/auth\/(me|status)$/.test(url) }
function cookieMutation(url, method) { return method === 'POST' && /\/auth\/(login|logout|password)$/.test(url) }
function requestDeadlineMs(url, method) {
  if (/\/admin\/(knowledge\/query|analyze|wiki\/ingest)$/.test(url)) return 310000
  return method === 'GET' || method === 'HEAD' ? 15000 : 60000
}
function current(port, message, record) {
  return ports.has(port) && !record.controller.signal.aborted && message.generation === generations.get(port)
}
function abortRecord(record, code, silent = false) {
  // An issued Cookie request must settle on the network before the lane opens.
  if (record.cookieIssued) { record.silent ||= silent; return }
  record.code = code
  record.silent ||= silent
  record.controller.abort()
}
function cancelOrdinary() {
  for (const entries of requests.values()) for (const record of entries.values()) {
    if (!record.mutation) abortRecord(record, 'identity_changed')
  }
}
function invalidateOthers(actor) {
  const id = ++noticeId
  const waiting = [...ports].filter((port) => port !== actor)
  if (!waiting.length) return Promise.resolve()
  return new Promise((resolve, reject) => {
    const pending = new Set(waiting)
    const finish = () => { clearTimeout(timeout); acknowledgements.delete(id); resolve() }
    const timeout = setTimeout(() => { acknowledgements.delete(id); reject({ code: 'coordination_unavailable' }) }, 5000)
    acknowledgements.set(id, { pending, finish })
    for (const port of waiting) {
      generations.set(port, (generations.get(port) || 0) + 1)
      try { port.postMessage({ type: 'invalidate', id }) } catch { /* Delivery failure cannot authorize a Cookie write. */ }
    }
  })
}
async function cancellable(record, work, deadline) {
  const signal = record.controller.signal
  if (signal.aborted) throw { code: record.code || 'cancelled' }
  let timer, onAbort
  const aborted = new Promise((_, reject) => {
    onAbort = () => reject({ code: record.code || 'cancelled' })
    signal.addEventListener('abort', onAbort, { once: true })
    timer = setTimeout(() => abortRecord(record, 'request_timeout'), deadline)
  })
  try { return await Promise.race([work(), aborted]) }
  finally { clearTimeout(timer); signal.removeEventListener('abort', onAbort) }
}
async function checkedIdentity(expected, record) {
  return cancellable(record, async () => {
    const response = await fetch('/api/console/v1/auth/me', { signal: record.controller.signal, credentials: 'same-origin', cache: 'no-store' })
    if (response.status === 401) return false
    if (!response.ok) {
      const payload = await response.json().catch(() => null)
      throw { status: response.status, code: typeof payload?.error?.code === 'string' ? payload.error.code : 'unknown' }
    }
    const actual = await response.json()
    return actual?.user?.id === expected?.userId && actual?.csrf_token === expected?.csrfToken
  }, 10000)
}
function failure(cause) {
  return { status: typeof cause?.status === 'number' ? cause.status : 0, code: typeof cause?.code === 'string' ? cause.code : 'network' }
}
async function waitForSockets() {
  const active = [...leases.values()].flatMap((entries) => [...entries.values()])
  for (const lease of active) lease.close()
  await Promise.all(active.map((lease) => lease.settled))
  if (coordinationFailed) throw { code: 'coordination_unavailable' }
}
async function perform(port, message, record) {
  const { id, url, method, body, expected } = message
  try {
    if (!current(port, message, record)) throw { status: 401, code: 'identity_changed' }
    if (coordinationFailed || (!record.mutation && cookiePending && expected && !publicIdentityURL(url))) throw { code: 'coordination_unavailable' }
    if (record.mutation) {
      // The entry gate was closed synchronously when this mutation was received.
      cancelOrdinary()
      await Promise.all([invalidateOthers(port), waitForSockets()])
    }
    if (expected && !publicIdentityURL(url) && !(await checkedIdentity(expected, record))) throw { status: 401, code: 'identity_changed' }
    if (!current(port, message, record)) throw { status: 401, code: 'identity_changed' }
    const headers = new Headers(message.headers)
    headers.delete('X-Console-Expected-User')
    if (expected && !publicIdentityURL(url)) {
      headers.set('X-Console-Expected-User', expected.userId)
      headers.set('X-CSRF-Token', expected.csrfToken)
    }
    const fetchResult = async () => {
      const response = await fetch(url, { method, headers, body, credentials: 'same-origin', cache: 'no-store', ...(record.mutation ? {} : { signal: record.controller.signal }) })
      return { status: response.status, body: await response.text() }
    }
    record.cookieIssued = record.mutation
    const result = record.mutation ? await fetchResult() : await cancellable(record, fetchResult, requestDeadlineMs(url, method))
    if (!record.silent) {
      if (!current(port, message, record)) reply(port, id, { status: 401, code: 'identity_changed' })
      else reply(port, id, result)
    }
  } catch (cause) { if (!record.silent) reply(port, id, failure(cause)) }
  finally { requests.get(port)?.delete(id) }
}
async function leaseSocket(port, message, record) {
  const { id, expected } = message
  try {
    if (cookiePending || coordinationFailed) throw { code: 'coordination_unavailable' }
    if (!current(port, message, record)) throw { status: 401, code: 'identity_changed' }
    if (!(await checkedIdentity(expected, record))) throw { status: 401, code: 'identity_changed' }
    if (!current(port, message, record)) throw { status: 401, code: 'identity_changed' }
    if (cookiePending) throw { code: 'coordination_unavailable' }
    let finish, closeTimer
    const settled = new Promise((resolve) => { finish = resolve })
    const release = () => {
      clearTimeout(handshakeTimer); clearTimeout(closeTimer)
      const entries = leases.get(port)
      entries?.delete(id)
      if (!ports.has(port) && !entries?.size) leases.delete(port)
      finish()
    }
    const close = () => {
      if (closeTimer) return
      try { port.postMessage({ type: 'close_socket', id }) } catch { /* Port destruction cannot prove WebSocket closure. */ }
      closeTimer = setTimeout(() => { coordinationFailed = true; release() }, 5000)
    }
    const handshakeTimer = setTimeout(close, 10000)
    leases.get(port).set(id, { settled, release, close })
    reply(port, id, { status: 200, lease: true })
  } catch (cause) { if (!record.silent) reply(port, id, failure(cause)) }
  finally { requests.get(port)?.delete(id) }
}
function removePort(port) {
  for (const record of requests.get(port)?.values() || []) abortRecord(record, 'cancelled', true)
  // bye / port close is not proof that a CONNECTING WebSocket has closed.
  // Retain orphan leases in the Cookie barrier until the close deadline fails closed.
  for (const lease of leases.get(port)?.values() || []) lease.close()
  ports.delete(port); generations.delete(port); requests.delete(port)
  if (!leases.get(port)?.size) leases.delete(port)
  for (const ack of acknowledgements.values()) { ack.pending.delete(port); if (!ack.pending.size) ack.finish() }
  port.close()
}
onconnect = (event) => {
  const port = event.ports[0]
  ports.add(port); generations.set(port, 0); requests.set(port, new Map()); leases.set(port, new Map())
  port.onmessage = (received) => {
    const message = received.data
    if (message?.type === 'hello') {
      port.postMessage({ type: 'ready', version: VERSION, generation: generations.get(port) })
      if (message.version !== VERSION) removePort(port)
      return
    }
    if (!ports.has(port)) return
    if (message?.type === 'bye') { removePort(port); return }
    if (message?.type === 'ack') {
      const ack = acknowledgements.get(message.id)
      if (ack) { ack.pending.delete(port); if (!ack.pending.size) ack.finish() }
      return
    }
    if (message?.type === 'release') { leases.get(port)?.get(message.id)?.release(); return }
    if (message?.type === 'socket_failed') { coordinationFailed = true; leases.get(port)?.get(message.id)?.close(); return }
    if (message?.type === 'cancel') {
      const record = requests.get(port)?.get(message.id)
      if (record) abortRecord(record, 'cancelled', true)
      // Cancelling a granted lease requests closure; it cannot prove closure.
      leases.get(port)?.get(message.id)?.close()
      return
    }
    if (message?.type !== 'request' && message?.type !== 'socket') return
    if (requests.get(port).has(message.id) || leases.get(port).has(message.id)) return
    const mutation = message.type === 'request' && cookieMutation(message.url, message.method)
    const record = { controller: new AbortController(), mutation, cookieIssued: false, silent: false, code: '' }
    requests.get(port).set(message.id, record)
    if (mutation) {
      cookiePending += 1
      cancelOrdinary()
      const run = async () => { try { await perform(port, message, record) } finally { cookiePending -= 1 } }
      cookieTail = cookieTail.then(run, run)
    } else if (message.type === 'socket') void leaseSocket(port, message, record)
    else void perform(port, message, record)
  }
  port.start()
}
