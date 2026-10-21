import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createProposalReader } from './proposalReads'
vi.mock('../coordinator', () => ({ coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options) }))
let releases: ((response: Response) => void)[]
beforeEach(() => {
  releases = []
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => releases.push(resolve))))
})
afterEach(() => vi.unstubAllGlobals())
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve() }
it('serializes an ID across callers while allowing a different ID to use the second slot', async () => {
  const read = createProposalReader(); const signal = new AbortController().signal
  const first = read('p', signal); const same = read('p', signal); const other = read('q', signal)
  expect(vi.mocked(fetch).mock.calls.map(([url]) => String(url).split('/').at(-1))).toEqual(['p', 'q'])
  releases[0](Response.json({ value: 'first' })); await first; await flush()
  expect(vi.mocked(fetch).mock.calls.map(([url]) => String(url).split('/').at(-1))).toEqual(['p', 'q', 'p'])
  releases[1](Response.json({ value: 'other' })); releases[2](Response.json({ value: 'same' }))
  expect(await other).toEqual({ value: 'other' }); expect(await same).toEqual({ value: 'same' })
})
it('removes cancelled queued work and retains running slots until aborted transport actually settles', async () => {
  const read = createProposalReader(); const old = new AbortController(); const next = new AbortController()
  const first = read('p', old.signal); const second = read('q', old.signal)
  const queued = read('retired', old.signal).catch((cause: unknown) => cause)
  old.abort()
  const third = read('new', next.signal)
  expect(await queued).toBeInstanceOf(DOMException)
  expect(fetch).toHaveBeenCalledTimes(2)
  releases[0](Response.json({ value: 'late' })); await first; await flush()
  expect(vi.mocked(fetch).mock.calls.map(([url]) => String(url).split('/').at(-1))).toEqual(['p', 'q', 'new'])
  releases[1](Response.json({})); releases[2](Response.json({ value: 'current' }))
  await second; expect(await third).toEqual({ value: 'current' })
})
