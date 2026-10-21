import { readdirSync, mkdirSync, mkdtempSync, realpathSync, lstatSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawn } from 'node:child_process'
import { createServer } from 'node:net'

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const cli = path.join(webRoot, 'node_modules/@playwright/test/cli.js')
const productRoot = path.resolve(webRoot, '..')
const tempParent = path.resolve(productRoot, '.tmp')
mkdirSync(tempParent, { recursive: true })
const tempParentReal = realpathSync(tempParent)
if (path.relative(realpathSync(productRoot), tempParentReal) !== '.tmp') throw new Error('Fixture temp parent escapes product .tmp')
const ownedRoots = new Map()
function createBatchRoot() {
  const root = mkdtempSync(path.join(tempParentReal, 'console-e2e-'))
  const info = lstatSync(root)
  ownedRoots.set(root, { dev: info.dev, ino: info.ino })
  return root
}
function removeBatchRoot(root) {
  const owned = ownedRoots.get(root)
  if (!owned || !path.isAbsolute(root) || path.dirname(path.resolve(root)) !== tempParentReal) throw new Error('Unowned fixture batch root')
  const info = lstatSync(root)
  if (path.relative(root, realpathSync(root)) !== '' || info.isSymbolicLink() || !info.isDirectory() || info.dev !== owned.dev || info.ino !== owned.ino) throw new Error('Fixture batch root ownership changed; preserving directory')
  // Only this exact mkdtemp directory is recursively removed after TCP proof.
  rmSync(root, { recursive: true, force: false, maxRetries: 3, retryDelay: 100 })
  ownedRoots.delete(root)
}
const args = process.argv.slice(2)
// Each file gets the existing disposable fixture and real login limiter.
// Sharing one server makes unrelated suites consume the same ten-attempt bucket.
const batches = args.length ? [args] : readdirSync(path.join(webRoot, 'e2e'))
  .filter(name => name.endsWith('.spec.ts')).sort()
  .map(name => [`e2e/${name}`, `--output=test-results/${name.replace('.spec.ts', '')}`])
let failed = false
let cancelledSignal, activeChild, stopping, stopFailure, forceTimer
async function stopOwnedChild(child, signal) {
  if (!Number.isInteger(child.pid) || child.pid <= 0) return
  if (process.platform === 'win32') {
    // Scope /T strictly to the PID directly spawned by this runner.
    const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { shell: false, windowsHide: true, stdio: ['ignore', 'ignore', 'inherit'] })
    const status = await new Promise((resolve, reject) => {
      killer.once('error', reject)
      killer.once('exit', code => resolve(code))
    })
    // A concurrent natural exit may mean taskkill no longer finds the PID.
    if (status !== 0 && activeChild === child) child.kill('SIGTERM')
  } else {
    const killGroup = value => {
      try { process.kill(-child.pid, value) } catch (error) { if (error.code !== 'ESRCH') throw error }
    }
    killGroup(signal)
    forceTimer = setTimeout(() => {
      try { killGroup('SIGKILL') } catch (error) { stopFailure = error }
    }, 5000)
    forceTimer.unref?.()
  }
}
function cancel(signal) {
  cancelledSignal ||= signal
  if (!activeChild || stopping) return
  const child = activeChild
  stopping = stopOwnedChild(child, signal).catch(error => {
    stopFailure = error
    child.kill('SIGTERM')
  })
}
const onInterrupt = () => cancel('SIGINT')
const onTerminate = () => cancel('SIGTERM')
process.on('SIGINT', onInterrupt)
process.on('SIGTERM', onTerminate)
async function probeFixtureStopped() {
  const probe = createServer()
  await new Promise((resolve, reject) => {
    probe.once('error', reject)
    probe.listen({ host: '127.0.0.1', port: 18081, exclusive: true }, resolve)
  })
  await new Promise((resolve, reject) => probe.close(error => error ? reject(error) : resolve()))
}
async function assertFixtureStopped() {
  // A graceful signal may stop the CLI before its server releases the socket.
  // Retain the Linux escalation timer until cleanup is actually proven.
  const deadline = Date.now() + (cancelledSignal ? 6000 : 0)
  for (;;) {
    try { await probeFixtureStopped(); return } catch (error) {
      if (!cancelledSignal || error.code !== 'EADDRINUSE' || Date.now() >= deadline) throw error
      await new Promise(resolve => setTimeout(resolve, 100))
    }
  }
}
try {
  for (const batch of batches) {
    if (cancelledSignal) break
    const fixtureRoot = createBatchRoot()
    let result
    try {
      const child = spawn(process.execPath, [cli, 'test', ...batch], { cwd: webRoot, stdio: 'inherit', shell: false, detached: process.platform !== 'win32', windowsHide: true, env: { ...process.env, CONSOLE_FIXTURE_TEMP_ROOT: fixtureRoot } })
      activeChild = child
      result = await new Promise(resolve => {
        child.once('error', error => resolve({ error }))
        child.once('exit', (code, signal) => resolve({ code, signal }))
      })
    } catch (error) { result = { error } }
    activeChild = undefined
    await stopping
    // Cancellation also waits for this proof; it cannot advance to another file.
    await assertFixtureStopped()
    clearTimeout(forceTimer)
    removeBatchRoot(fixtureRoot)
    if (result.error) throw result.error
    if (stopFailure) throw stopFailure
    if (cancelledSignal) break
    if (result.signal) throw new Error(`Browser tests interrupted: ${result.signal}`)
    if (result.code !== 0) failed = true
  }
  process.exitCode = cancelledSignal ? cancelledSignal === 'SIGINT' ? 130 : 143 : failed ? 1 : 0
} finally {
  clearTimeout(forceTimer)
  process.removeListener('SIGINT', onInterrupt)
  process.removeListener('SIGTERM', onTerminate)
}
