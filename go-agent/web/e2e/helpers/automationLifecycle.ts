import { expect, type Locator } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'

export const writeKeys = ['total', 'config', 'reload', 'enable', 'disable', 'delete', 'device', 'other'] as const
export type Writes = Record<typeof writeKeys[number], number>
type Mode = Partial<{
  history: 'empty' | 'pattern' | 'forbidden'; profile: 'supported' | 'unknown'
  config: 'available' | 'forbidden' | 'drift'; runtime: 'available' | 'missing' | 'drift'
  registry: 'available' | 'forbidden' | 'missing'; trace: 'finished' | 'empty' | 'forbidden' | 'unsupported' | 'retention_gap'
  state: 'observed' | 'on' | 'off' | 'unavailable'; fault: 'none' | 'lose_config' | 'reject_config' | 'lose_reload' | 'lose_management'
  history_delay_ms: number; read_delay_ms: number
}>
type State = { modes: Mode; writes: Writes; history_reads: number }

// Only this Node closure sees the disposable per-process capability. Never
// publish the descriptor, pass it into page JS, or accept arbitrary URLs/files.
export async function automationLifecycle() {
  if (process.env.CONSOLE_AUTOMATION_LIFECYCLE !== '1') throw new Error('Opt-in lifecycle fixture required')
  const root = process.env.CONSOLE_FIXTURE_TEMP_ROOT
  if (!root || !path.isAbsolute(root)) throw new Error('Runner-owned fixture root required')
  expect((await fs.lstat(root)).isSymbolicLink()).toBe(false)
  const realRoot = await fs.realpath(root)
  expect(realRoot).toBe(path.resolve(root))
  const descriptorPath = path.join(realRoot, 'automation-lifecycle.json')
  const info = await fs.lstat(descriptorPath)
  expect(info.isFile() && !info.isSymbolicLink()).toBe(true)
  expect(info.size).toBeLessThan(1024)
  expect(await fs.realpath(descriptorPath)).toBe(descriptorPath)
  const descriptor = JSON.parse(await fs.readFile(descriptorPath, 'utf8'))
  expect(descriptor.schema_version).toBe(1)
  expect(descriptor.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/)
  const url = new URL(descriptor.url)
  expect(Number(url.port)).toBeGreaterThan(0)
  expect(Number(url.port)).toBeLessThanOrEqual(65535)
  expect(descriptor.capability).toMatch(/^[a-f0-9]{64}$/)
  async function request(route: '/state' | '/mode', mode?: Mode) {
    const response = await fetch(descriptor.url + route, {
      method: mode ? 'PUT' : 'GET',
      headers: { Authorization: `Bearer ${descriptor.capability}`, ...(mode ? { 'Content-Type': 'application/json' } : {}) },
      ...(mode ? { body: JSON.stringify(mode) } : {}),
    })
    expect(response.status).toBe(200)
    expect(response.headers.get('cache-control')).toBe('no-store')
    return response.json()
  }
  return {
    state: async (): Promise<State> => request('/state'),
    // Replaces the complete mode; omitted fields intentionally return to defaults.
    mode: async (mode: Mode) => { await request('/mode', mode) },
  }
}

export function expectWrites(before: Writes, after: Writes, delta: Partial<Writes> = {}) {
  expect(after).toEqual(Object.fromEntries(writeKeys.map(key => [key, before[key] + (delta[key] ?? 0)])))
}

export async function keyboardSelect(select: Locator, label: string) {
  const index = (await select.locator('option').allTextContents()).indexOf(label)
  expect(index).toBeGreaterThan(0)
  await select.focus()
  await select.press('Home')
  for (let i = 0; i < index; i++) await select.press('ArrowDown')
  await select.press('Enter')
  await expect(select.locator('option:checked')).toHaveText(label)
}
