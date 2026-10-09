// The URL and worker name must remain stable across frontend versions.
const VERSION = 1
const ports = new Set()
const generations = new Map()
const acknowledgements = new Map()
const cancelled = new Map()
const leases = new Map()
let tail = Promise.resolve()
let noticeId = 0

function reply(port, id, result) {
  try { port.postMessage({ type: 'result', id, ...result }) } catch { /* Fail closed if the page vanished. */ }
}

function invalidateOthers(actor) {
  const id = ++noticeId
  const waiting = [...ports].filter((port) => port !== actor)
  if (!waiting.length) return Promise.resolve()
  return new Promise((resolve, reject) => {
    const pending = new Set(waiting)
    const finish = () => { clearTimeout(timeout); acknowledgements.delete(id); resolve() }
    const timeout = setTimeout(() => { acknowledgements.delete(id); reject(new Error('ack_timeout')) }, 5000)
    acknowledgements.set(id, { pending, finish })
    for (const port of waiting) {
      generations.set(port, (generations.get(port) || 0) + 1)
      try { port.postMessage({ type: 'invalidate', id }) } catch { /* A failed delivery cannot authorize a Cookie write. */ }
    }
  })
}

async function checkedIdentity(expected) {
  const response = await fetch('/api/console/v1/auth/me', { credentials: 'same-origin', cache: 'no-store' })
  if (response.status === 401) return false
  if (!response.ok) {
    const payload = await response.json().catch(() => null)
    throw { status: response.status, code: typeof payload?.error?.code === 'string' ? payload.error.code : 'unknown' }
  }
  const actual = await response.json()
  return actual?.user?.id === expected?.userId && actual?.csrf_token === expected?.csrfToken
}


function identityFailure(cause) {
  return {
    status: typeof cause?.status === 'number' ? cause.status : 0,
    code: typeof cause?.code === 'string' ? cause.code : 'network',
  }
}
async function perform(port, message) {
  const { id, url, method, headers, body, generation, expected, mutation } = message
  if (cancelled.get(port)?.delete(id)) return
  if (generation !== generations.get(port)) return reply(port, id, { status: 0, code: 'identity_changed' })
  try {
    if (expected && !(await checkedIdentity(expected))) return reply(port, id, { status: 401, code: 'identity_changed' })
  } catch (cause) { return reply(port, id, identityFailure(cause)) }
  if (mutation) {
    try { await invalidateOthers(port) }
    catch { return reply(port, id, { status: 0, code: 'coordination_unavailable' }) }
  }
  if (generation !== generations.get(port)) return reply(port, id, { status: 0, code: 'identity_changed' })
  if (cancelled.get(port)?.delete(id)) return
  try {
    const response = await fetch(url, { method, headers, body, credentials: 'same-origin', cache: 'no-store' })
    const text = await response.text()
    reply(port, id, { status: response.status, body: text })
  } catch { reply(port, id, { status: 0, code: 'network' }) }
}

async function leaseSocket(port, message) {
  const { id, generation, expected } = message
  if (cancelled.get(port)?.delete(id)) return
  if (generation !== generations.get(port)) return reply(port, id, { status: 0, code: 'identity_changed' })
  try {
    if (!(await checkedIdentity(expected))) return reply(port, id, { status: 401, code: 'identity_changed' })
  } catch (cause) { return reply(port, id, identityFailure(cause)) }
  if (cancelled.get(port)?.delete(id)) return
  if (!ports.has(port) || generation !== generations.get(port)) return reply(port, id, { status: 0, code: 'identity_changed' })
  await new Promise((resolve) => {
    const onRelease = (event) => {
      if (event.data?.type !== 'release' || event.data.id !== id) return
      finish()
    }
    const finish = () => { port.removeEventListener('message', onRelease); leases.delete(id); resolve() }
    leases.set(id, { port, finish })
    port.addEventListener('message', onRelease)
    reply(port, id, { status: 200, lease: true })
    // A missing release deliberately blocks later Cookie mutations.
  })
}

onconnect = (event) => {
  const port = event.ports[0]
  ports.add(port)
  generations.set(port, 0)
  cancelled.set(port, new Set())
  port.onmessage = (received) => {
    const message = received.data
    if (message?.type === 'hello') {
      port.postMessage({ type: 'ready', version: VERSION, generation: generations.get(port) })
      if (message.version !== VERSION) {
        ports.delete(port); generations.delete(port); cancelled.delete(port); port.close()
      }
      return
    }
    if (message?.type === 'bye') {
      for (const lease of leases.values()) if (lease.port === port) lease.finish()
      ports.delete(port)
      generations.delete(port)
      cancelled.delete(port)
      for (const ack of acknowledgements.values()) {
        ack.pending.delete(port)
        if (!ack.pending.size) ack.finish()
      }
      port.close()
      return
    }
    if (message?.type === 'ack') {
      const ack = acknowledgements.get(message.id)
      if (ack) { ack.pending.delete(port); if (!ack.pending.size) ack.finish() }
      return
    }
    if (message?.type === 'cancel') {
      cancelled.get(port)?.add(message.id)
      const lease = leases.get(message.id)
      if (lease?.port === port) lease.finish()
      return
    }
    if (message?.type !== 'request' && message?.type !== 'socket') return
    const run = () => message.type === 'socket' ? leaseSocket(port, message) : perform(port, message)
    tail = tail.then(run, run)
  }
  port.start()
}
