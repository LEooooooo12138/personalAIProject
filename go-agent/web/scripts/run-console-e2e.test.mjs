import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import { EventEmitter } from 'node:events'
import nodePath from 'node:path'
import { test } from 'node:test'
import assert from 'node:assert/strict'
const source = readFileSync(new URL('./run-console-e2e.mjs', import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve() }
function harness(platform) {
  const process = new EventEmitter()
  Object.assign(process, { argv: ['node', 'runner'], platform, execPath: 'node', env: { KEEP: 'ambient', CONSOLE_FIXTURE_TEMP_ROOT: '/foreign' }, exitCode: undefined })
  const children = [], kills = [], probes = [], removed = [], roots = [], events = []
  let changedRoot = false
  const mkdirSync = () => {}
  const mkdtempSync = prefix => { const root = prefix + roots.length; roots.push(root); return root }
  const realpathSync = value => changedRoot && roots.includes(value) ? '/foreign' : value
  const lstatSync = value => ({ dev: 1, ino: roots.indexOf(value) + 1, isDirectory: () => true, isSymbolicLink: () => false })
  const rmSync = (value, options) => { removed.push({ value, options }); events.push('remove') }
  const stop = (child, signal) => queueMicrotask(() => child.emit('exit', null, signal))
  process.kill = (pid, signal) => { kills.push({ pid, signal }); stop(children.find(child => child.pid === Math.abs(pid)), signal) }
  const spawn = (command, args, options) => {
    const child = Object.assign(new EventEmitter(), { pid: 100 + children.length, exitCode: null, signalCode: null, kill: signal => stop(child, signal) })
    if (command === 'taskkill') {
      kills.push({ command, args, options })
      stop(children.find(value => String(value.pid) === args[args.indexOf('/PID') + 1]), 'SIGTERM')
      queueMicrotask(() => child.emit('exit', 0, null))
    } else { child.options = options; child.once('exit', () => events.push('child-exit')); children.push(child) }
    return child
  }
  const createServer = () => {
    const server = Object.assign(new EventEmitter(), {
      listen: (options, callback) => { probes.push({ options, finish: callback }); return server },
      close: callback => { events.push('port-proof'); callback(); return server },
    })
    return server
  }
  const script = source.replaceAll('import.meta.url', "'file:///work/web/scripts/run-console-e2e.mjs'")
  const done = runInNewContext(`(async () => {${script}\n})()`, { process, spawn, createServer, mkdirSync, mkdtempSync, realpathSync, lstatSync, rmSync, readdirSync: () => ['one.spec.ts', 'two.spec.ts'], path: nodePath.posix, fileURLToPath: () => '/work/web/scripts/run-console-e2e.mjs', setTimeout, clearTimeout })
  return { process, done, children, kills, probes, roots, removed, events, replaceRoot: () => { changedRoot = true } }
}
for (const [platform, signal, code] of [['win32', 'SIGINT', 130], ['linux', 'SIGTERM', 143]]) {
  test(`${platform} ${signal} cancellation stops only the owned child and awaits port release`, async () => {
    const runner = harness(platform)
    assert.equal(runner.children.length, 1)
    assert.equal(runner.process.emit(signal), true, 'runner must handle parent cancellation')
    await flush()
    assert.equal(runner.children.length, 1, 'cancel must never start next file')
    assert.equal(runner.probes.length, 1, 'child termination must reach TCP bind proof')
    assert.equal(runner.process.exitCode, undefined, 'port proof must precede cancelled exit')
    if (platform === 'win32') {
      assert.equal(runner.kills[0].command, 'taskkill')
      assert.deepEqual([...runner.kills[0].args], ['/PID', '100', '/T', '/F'])
      assert.equal(runner.kills[0].options.windowsHide, true)
    } else {
      assert.equal(runner.kills[0].pid, -100)
      assert.equal(runner.kills[0].signal, 'SIGTERM')
    }
    runner.probes[0].finish()
    await runner.done
    assert.equal(runner.process.exitCode, code)
    assert.equal(runner.children.length, 1)
  })
}


test('normal batches retain failure status and cannot advance before port proof', async () => {
  const runner = harness('win32')
  runner.children[0].emit('exit', 1, null)
  await flush()
  assert.equal(runner.children.length, 1)
  assert.equal(runner.probes.length, 1)
  runner.probes[0].finish()
  await flush()
  assert.equal(runner.children.length, 2)
  runner.children[1].emit('exit', 0, null)
  await flush()
  runner.probes[1].finish()
  await runner.done
  assert.equal(runner.process.exitCode, 1)
  assert.equal(runner.kills.length, 0)
})

test('signal after child exit still awaits pending port proof and stops later files', async () => {
  const runner = harness('win32')
  runner.children[0].emit('exit', 0, null)
  await flush()
  assert.equal(runner.probes.length, 1)
  runner.process.emit('SIGTERM')
  assert.equal(runner.process.exitCode, undefined)
  runner.probes[0].finish()
  await runner.done
  assert.equal(runner.process.exitCode, 143)
  assert.equal(runner.children.length, 1)
  assert.equal(runner.kills.length, 0)
})


test('success and failed files delete only their own root after child exit and TCP proof', async () => {
 const runner = harness('win32')
 assert.equal(runner.roots.length, 1)
 assert.equal(runner.children[0].options.env.CONSOLE_FIXTURE_TEMP_ROOT, runner.roots[0])
 assert.equal(runner.children[0].options.env.KEEP, 'ambient')
 runner.children[0].emit('exit', 0, null); await flush()
 assert.equal(runner.removed.length, 0)
 runner.probes[0].finish(); await flush()
 assert.equal(runner.removed.length, 1)
 assert.equal(runner.removed[0].value, runner.roots[0])
 assert.equal(runner.roots.length, 2)
 runner.children[1].emit('exit', 1, null); await flush()
 assert.equal(runner.removed.length, 1)
 runner.probes[1].finish(); await runner.done
 assert.equal(runner.removed[1].value, runner.roots[1])
 assert.equal(runner.process.exitCode, 1)
 assert.deepEqual(runner.events, ['child-exit','port-proof','remove','child-exit','port-proof','remove'])
})

test('cancel deletes the owned root after termination and proof without advancing', async () => {
 const runner = harness('win32')
 runner.process.emit('SIGTERM'); await flush()
 assert.equal(runner.removed.length, 0)
 runner.probes[0].finish(); await runner.done
 assert.equal(runner.removed.length, 1)
 assert.equal(runner.removed[0].value, runner.roots[0])
 assert.equal(runner.children.length, 1)
 assert.equal(runner.process.exitCode, 143)
 assert.deepEqual(runner.events, ['child-exit','port-proof','remove'])
})

test('changed root ownership fails closed without recursive deletion', async () => {
 const runner = harness('win32')
 runner.replaceRoot()
 runner.children[0].emit('exit', 0, null); await flush()
 const rejection = assert.rejects(runner.done, /owned|ownership|batch root/i)
 runner.probes[0].finish(); await flush()
 assert.equal(runner.children.length, 1, 'ownership failure must never advance')
 await rejection
 assert.equal(runner.removed.length, 0)
 assert.equal(runner.children.length, 1)
})
