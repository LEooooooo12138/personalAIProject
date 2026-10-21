import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { App } from '../App'

const base = { phase: 'idle', completed_batches: 0, total_batches: 0, target_days: 14, retention: 'unknown', next_analysis_at: null, analysis_runs: [], backfill_runs: [] }
const identity = { user: { id: 'owner', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }, csrf_token: 'csrf-test', capabilities: ['collection:read', 'suggestions:manage', 'sessions:read'] }
const window = { start: '2026-10-01T00:00:00Z', end: '2026-10-15T00:00:00Z' }
const result = (status = 'running', outcome = '') => ({ id: 'run-one', status, requested_window: window, home_timezone: 'UTC', created_at: window.end, algorithm_version: 'deterministic-v1', outcome, counts: { entries: 0, patterns: 0, suggestions: 0, excluded: {} }, outcomes: {}, coverage: { requested_union_duration: 0, successful_union_duration: 0, entities: {}, corrupt_files: 0 } })
function show(read: (options: RequestInit) => Response | Promise<Response> = () => Response.json(result()), collection: Record<string, unknown> = base) {
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  const path = url.replace('/api/console/v1', '')
  if (path === '/auth/me') return Response.json(identity)
  if (path === '/sessions') return Response.json({ sessions: [] })
  if (path === '/admin/collection') return Response.json(collection)
  if (path === '/admin/analyze') return Response.json({ run_id: 'run-one', status: 'queued' }, { status: 202 })
  if (path === '/admin/analysis-runs/run-one/cancel') return Response.json({ run_id: 'run-one', status: 'running', cancel_requested: true }, { status: 202 })
  if (path === '/admin/analysis-runs/run-one') return read(options)
  if (path === '/auth/logout') return new Response(null, { status: 204 })
  return Response.json({ error: { code: 'not_found' } }, { status: 404 })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 return requests
}
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }) })
it.each(['insufficient_data', 'no_pattern'])('distinguishes %s without automatic starts', async outcome => {
 const requests = show(() => Response.json(result('completed', outcome)))
 await screen.findByRole('button', { name: '分析现有数据' })
 expect(requests.mock.calls.some(([url]) => url.endsWith('/admin/analyze'))).toBe(false)
 fireEvent.click(screen.getByRole('button', { name: '分析现有数据' }))
 expect(await screen.findByText(outcome === 'insufficient_data' ? /数据不足，当前算法无法给出结论/ : /未发现符合当前门槛的规律/)).toBeInTheDocument()
 expect(screen.getByText(/保留能力未知/)).toBeInTheDocument()
})
it('polls 2/5/10 seconds, pauses hidden, and never treats cancel acceptance as terminal', async () => {
 const requests = show()
 await screen.findByRole('button', { name: '分析现有数据' })
 vi.useFakeTimers()
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '分析现有数据' })) })
 const reads = () => requests.mock.calls.filter(([url]) => url.endsWith('/analysis-runs/run-one')).length
 expect(reads()).toBe(1)
 await act(async () => { await vi.advanceTimersByTimeAsync(1999) }); expect(reads()).toBe(1)
 await act(async () => { await vi.advanceTimersByTimeAsync(1) }); expect(reads()).toBe(2)
 await act(async () => { await vi.advanceTimersByTimeAsync(5000) }); expect(reads()).toBe(3)
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '取消分析' })) })
 expect(screen.getByText(/已请求取消，等待任务状态确认/)).toBeInTheDocument()
 expect(screen.queryByText('分析已取消')).not.toBeInTheDocument()
 Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
 await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(30000) })
 expect(reads()).toBe(3)
 Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
 await act(async () => { document.dispatchEvent(new Event('visibilitychange')) }); expect(reads()).toBe(4)
 expect(requests.mock.calls.filter(([url]) => url.endsWith('/admin/analyze'))).toHaveLength(1)
})



it('shows scoped hit evidence with its denominator and never calls it success probability', async () => {
 vi.stubGlobal('fetch', vi.fn(async (url: string) => {
  if (url.endsWith('/auth/me')) return Response.json({ ...identity, capabilities: [...identity.capabilities, 'suggestions:read'] })
  if (url.endsWith('/suggestions')) return Response.json({ count: 1, suggestions: [{ id: 'evidence', title: '建议', status: 'pending', created_at: window.end, confidence: 11/14, time_zone: 'UTC', entity_ids: ['light.room'], rule: null, missing_bindings: [], unsupported_code: null, shared: false, can_bind: false, can_confirm: false, can_ignore: false, evidence: { sample_size: 11, period_days: 14, hit_days: 11, eligible_days: 14, denominator: 14, denominator_type: 'requested_days', window, algorithm_version: 'deterministic-v1' } }] })
  if (url.endsWith('/admin/sharing')) return Response.json({ revision: 1, suggestion_ids: [] })
  return Response.json({ sessions: [] })
 }))
 render(<MemoryRouter initialEntries={['/app/automations?id=evidence']}><App/></MemoryRouter>)
 expect(await screen.findByText('历史命中率')).toBeInTheDocument()
 expect(screen.getByText('11 / 14 请求天数')).toBeInTheDocument()
 expect(screen.queryByText('成功概率')).not.toBeInTheDocument()
})


it('stops terminal polling and rejects a delayed run read after logout', async () => {
 let finish = false
 const requests = show(() => Response.json(result(finish ? 'completed' : 'running', finish ? 'no_pattern' : '')))
 await screen.findByRole('button', { name: '分析现有数据' })
 vi.useFakeTimers()
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '分析现有数据' })) })
 await act(async () => { await vi.advanceTimersByTimeAsync(2000); await vi.advanceTimersByTimeAsync(5000) })
 const reads = () => requests.mock.calls.filter(([url]) => url.endsWith('/analysis-runs/run-one')).length
 expect(reads()).toBe(3)
 await act(async () => { await vi.advanceTimersByTimeAsync(9999) }); expect(reads()).toBe(3)
 finish = true
 await act(async () => { await vi.advanceTimersByTimeAsync(1) }); expect(reads()).toBe(4)
 await act(async () => { await vi.advanceTimersByTimeAsync(30000) }); expect(reads()).toBe(4)
})
it('aborts in-flight reads on hidden and identity changes without publishing stale results', async () => {
 let release!: (value: Response) => void
 let readSignal: AbortSignal | undefined
 const requests = show(options => { readSignal = options.signal as AbortSignal; return new Promise<Response>(resolve => { release = resolve }) })
 await screen.findByRole('button', { name: '分析现有数据' })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '分析现有数据' })) })
 expect(readSignal?.aborted).toBe(false)
 Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
 await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
 expect(readSignal?.aborted).toBe(true)
 await act(async () => { fireEvent.click(screen.getAllByRole('button', { name: '退出登录' })[0]) })
 await screen.findByRole('button', { name: '登录' })
 await act(async () => { release(Response.json({ ...result('completed', 'matched'), counts: { suggestions: 999 } })) })
 expect(screen.queryByText(/本次建议 999/)).not.toBeInTheDocument()
 expect(requests.mock.calls.filter(([url]) => url.endsWith('/admin/analyze'))).toHaveLength(1)
})


it('resumes only the original stored backfill window after an explicit click', async () => {
 const oldWindow = { start: '2026-09-01T00:00:00Z', end: '2026-09-02T00:00:00Z' }
 const prior = { id: 'old-backfill', status: 'cancelled', requested_window: oldWindow, cursor: oldWindow.start, completed_batches: 1, gaps: [oldWindow], retention: 'unknown', created_at: oldWindow.end, updated_at: oldWindow.end, error_code: 'interrupted' }
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  if (url.endsWith('/auth/me')) return Response.json(identity)
  if (url.endsWith('/admin/collection')) return Response.json({ ...base, requested_window: window, backfill_runs: [prior] })
  if (url.endsWith('/admin/backfills') && options.method === 'POST') return Response.json({ run_id: prior.id, status: 'queued' }, { status: 202 })
  if (url.endsWith('/admin/backfills/old-backfill')) return Response.json(prior)
  return Response.json({ sessions: [] })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 await screen.findByRole('button', { name: '继续原窗口补采' })
 expect(requests.mock.calls.some(([url]) => url.endsWith('/admin/backfills'))).toBe(false)
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '继续原窗口补采' })) })
 const post = requests.mock.calls.find(([url]) => url.endsWith('/admin/backfills'))!
 expect(JSON.parse(post[1]!.body as string)).toEqual(oldWindow)
})


it('does not round a successful one-hour request window down to zero days', async () => {
 show(undefined, { ...base, coverage: { requested_union_duration: 3600000000000, successful_union_duration: 3600000000000, corrupt_files: 0, entities: {} } })
 expect((await screen.findAllByText('约 0.04 天')).length).toBe(2)
})

it.each(['analysis', 'backfill'] as const)('reconciles an accepted %s whose start response was lost without posting again', async kind => {
 const startPath = kind === 'analysis' ? '/admin/analyze' : '/admin/backfills'
 const runPath = kind === 'analysis' ? '/admin/analysis-runs' : '/admin/backfills'
 const startLabel = kind === 'analysis' ? '分析现有数据' : '补采最近 14 天'
 const cancelLabel = kind === 'analysis' ? '取消分析' : '取消补采'
 let accepted = false
 let requestedWindow = window
 const old = { ...result('completed', 'no_pattern'), id: 'older-run', source: 'manual', created_at: '2000-01-01T00:00:00Z' }
 const recovered = () => kind === 'analysis'
  ? { ...result(), id: 'recovered-run', source: 'manual', created_at: new Date(Date.now() + 10).toISOString() }
  : { id: 'recovered-run', status: 'running', requested_window: requestedWindow, cursor: requestedWindow.start, completed_batches: 0, gaps: [requestedWindow], retention: 'unknown', created_at: new Date(Date.now() + 10).toISOString(), updated_at: new Date(Date.now() + 10).toISOString() }
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  const path = url.replace('/api/console/v1', '')
  if (path === '/auth/me') return Response.json(identity)
  if (path === '/admin/collection') {
   const job = recovered()
   return Response.json(kind === 'analysis' ? { ...base, latest_analysis: accepted ? job : old, analysis_runs: accepted ? [job, old] : [old] } : { ...base, requested_window: window, backfill_runs: accepted ? [job] : [] })
  }
  if (path === startPath && options.method === 'POST') {
   requestedWindow = JSON.parse(options.body as string)
   accepted = true // The job exists on the server before its response is lost.
   throw new TypeError('response lost')
  }
  if (path === runPath + '/recovered-run/cancel') return Response.json({ run_id: 'recovered-run', status: 'running', cancel_requested: true }, { status: 202 })
  if (path === runPath + '/recovered-run') return Response.json(recovered())
  return Response.json({ sessions: [] })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 await screen.findByRole('button', { name: '刷新采集显示' })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: startLabel })) })
 expect(screen.getByRole('button', { name: startLabel })).toBeDisabled()
 expect(screen.queryByRole('button', { name: cancelLabel })).not.toBeInTheDocument()
 await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
 expect(screen.queryByRole('button', { name: cancelLabel })).not.toBeInTheDocument()
 expect(screen.getByRole('button', { name: startLabel })).toBeDisabled()
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '刷新采集显示' })) })
 expect(await screen.findByRole('button', { name: cancelLabel })).toBeEnabled()
 expect(requests.mock.calls.some(([url]) => url.endsWith(runPath + '/recovered-run'))).toBe(true)
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: cancelLabel })) })
 expect(requests.mock.calls.filter(([url, options]) => url.endsWith(startPath) && options?.method === 'POST')).toHaveLength(1)
 expect(requests.mock.calls.filter(([url]) => url.endsWith('/recovered-run/cancel'))).toHaveLength(1)
})


it.each(['stale', 'scheduled', 'ambiguous'] as const)('keeps an uncertain analysis blocked when refresh only offers %s records', async scenario => {
 let accepted = false
 const stale = { ...result(), id: 'unseen-old-run', source: 'manual', created_at: '2000-01-01T00:00:00Z' }
 const candidate = () => ({ ...result(), id: 'new-run', source: scenario === 'scheduled' ? 'scheduled' : 'manual', created_at: new Date(Date.now() + 10).toISOString() })
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  if (url.endsWith('/auth/me')) return Response.json(identity)
  if (url.endsWith('/admin/collection')) {
   const runs = !accepted ? [] : scenario === 'stale' ? [stale] : scenario === 'scheduled' ? [candidate()] : [candidate(), { ...candidate(), id: 'another-new-run' }]
   return Response.json({ ...base, latest_analysis: runs[0], analysis_runs: runs })
  }
  if (url.endsWith('/admin/analyze') && options.method === 'POST') { accepted = true; throw new TypeError('response lost') }
  return Response.json({ sessions: [] })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 await screen.findByRole('button', { name: '刷新采集显示' })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '分析现有数据' })) })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '刷新采集显示' })) })
 expect(screen.getByText(/暂未找到唯一匹配的任务/)).toBeInTheDocument()
 expect(screen.getByRole('button', { name: '分析现有数据' })).toBeDisabled()
 expect(screen.queryByRole('button', { name: '取消分析' })).not.toBeInTheDocument()
 expect(requests.mock.calls.filter(([url]) => url.includes('/admin/analysis-runs/'))).toHaveLength(0)
 expect(requests.mock.calls.filter(([url, options]) => url.endsWith('/admin/analyze') && options?.method === 'POST')).toHaveLength(1)
})
it('does not adopt a reconciliation response that arrives after logout', async () => {
 let accepted = false
 let release!: (value: Response) => void
 let pendingSignal: AbortSignal | undefined
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  if (url.endsWith('/auth/me')) return Response.json(identity)
  if (url.endsWith('/admin/collection')) {
   if (!accepted) return Response.json(base)
   pendingSignal = options.signal as AbortSignal
   return new Promise<Response>(resolve => { release = resolve })
  }
  if (url.endsWith('/admin/analyze') && options.method === 'POST') { accepted = true; throw new TypeError('response lost') }
  if (url.endsWith('/auth/logout')) return new Response(null, { status: 204 })
  return Response.json({ sessions: [] })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 await screen.findByRole('button', { name: '刷新采集显示' })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '分析现有数据' })) })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '刷新采集显示' })) })
 await act(async () => { fireEvent.click(screen.getAllByRole('button', { name: '退出登录' })[0]) })
 await screen.findByRole('button', { name: '登录' })
 expect(pendingSignal?.aborted).toBe(true)
 const stale = { ...result(), id: 'late-private-run', source: 'manual', created_at: new Date(Date.now() + 10).toISOString() }
 await act(async () => { release(Response.json({ ...base, latest_analysis: stale, analysis_runs: [stale] })) })
 expect(screen.queryByRole('button', { name: '取消分析' })).not.toBeInTheDocument()
 expect(requests.mock.calls.filter(([url]) => url.includes('/admin/analysis-runs/'))).toHaveLength(0)
})


it('reconciles a resumed backfill by its exact window and updated stable ID, not another window', async () => {
 const storedWindow = { start: '2026-09-01T00:00:00Z', end: '2026-09-02T00:00:00Z' }
 const old = { id: 'resume-run', status: 'cancelled', requested_window: storedWindow, cursor: storedWindow.start, completed_batches: 1, gaps: [storedWindow], retention: 'unknown', created_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:00Z' }
 let accepted = false
 let correctWindow = false
 const resumed = () => ({ ...old, status: 'running', updated_at: new Date(Date.now() + 10).toISOString() })
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  if (url.endsWith('/auth/me')) return Response.json(identity)
  if (url.endsWith('/admin/collection')) return Response.json({ ...base, backfill_runs: !accepted ? [old] : correctWindow ? [resumed()] : [{ ...resumed(), id: 'wrong-window', created_at: new Date(Date.now() + 10).toISOString(), requested_window: window }, old] })
  if (url.endsWith('/admin/backfills') && options.method === 'POST') { expect(JSON.parse(options.body as string)).toEqual(storedWindow); accepted = true; throw new TypeError('response lost') }
  if (url.endsWith('/admin/backfills/resume-run')) return Response.json(resumed())
  return Response.json({ sessions: [] })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 await screen.findByRole('button', { name: '继续原窗口补采' })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '继续原窗口补采' })) })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '刷新采集显示' })) })
 expect(screen.queryByRole('button', { name: '取消补采' })).not.toBeInTheDocument()
 expect(screen.getByRole('button', { name: '补采最近 14 天' })).toBeDisabled()
 correctWindow = true
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '刷新采集显示' })) })
 expect(await screen.findByRole('button', { name: '取消补采' })).toBeEnabled()
 expect(requests.mock.calls.filter(([url]) => url.endsWith('/admin/backfills/resume-run'))).toHaveLength(1)
 expect(requests.mock.calls.filter(([url, options]) => url.endsWith('/admin/backfills') && options?.method === 'POST')).toHaveLength(1)
})


it('uses the same reconciliation when retrying an initially failed collection read', async () => {
 let accepted = false
 const job = () => ({ ...result(), id: 'recovered-after-read-error', source: 'manual', created_at: new Date(Date.now() + 10).toISOString() })
 vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit = {}) => {
  if (url.endsWith('/auth/me')) return Response.json(identity)
  if (url.endsWith('/admin/collection')) return accepted ? Response.json({ ...base, latest_analysis: job(), analysis_runs: [job()] }) : Response.json({ error: { code: 'unavailable' } }, { status: 503 })
  if (url.endsWith('/admin/analyze') && options.method === 'POST') { accepted = true; throw new TypeError('response lost') }
  if (url.endsWith('/admin/analysis-runs/recovered-after-read-error')) return Response.json(job())
  return Response.json({ sessions: [] })
 }))
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 await screen.findByRole('button', { name: '重新读取采集状态' })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '分析现有数据' })) })
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '重新读取采集状态' })) })
 expect(await screen.findByRole('button', { name: '取消分析' })).toBeEnabled()
})

// Initial backfill must use the displayed household target, even when the
// browser clock differs. Only HTTP is replaced; App/actions remain real.
function showBackfillTarget(collection: Record<string, unknown>) {
 let requested = window
 const requests = vi.fn(async (url: string, options: RequestInit = {}) => {
  if (url.endsWith('/auth/me')) return Response.json(identity)
  if (url.endsWith('/admin/collection')) return Response.json(collection)
  if (url.endsWith('/admin/backfills') && options.method === 'POST') {
   requested = JSON.parse(options.body as string)
   return Response.json({ run_id: 'target-backfill', status: 'queued' }, { status: 202 })
  }
  if (url.endsWith('/admin/backfills/target-backfill')) return Response.json({ id: 'target-backfill', status: 'completed', requested_window: requested, cursor: requested.end, completed_batches: 1, gaps: [], retention: 'unknown', created_at: requested.end, updated_at: requested.end })
  return Response.json({ sessions: [] })
 })
 vi.stubGlobal('fetch', requests)
 render(<MemoryRouter initialEntries={['/app/admin/collection']}><App/></MemoryRouter>)
 return requests
}

it.each(['2026-10-16T12:00:00Z', '2031-04-01T23:47:00Z'])('submits the exact displayed household window despite browser time %s', async browserNow => {
 const target = { start: '2026-10-01T16:00:00Z', end: '2026-10-15T16:00:00Z' }
 const requests = showBackfillTarget({ ...base, requested_window: target, coverage_error: 'coverage_unavailable' })
 const button = await screen.findByRole('button', { name: '补采最近 14 天' })
 await waitFor(() => expect(button).toBeEnabled())
 expect(requests.mock.calls.some(([url]) => url.endsWith('/admin/backfills'))).toBe(false)
 vi.useFakeTimers({ toFake: ['Date'] })
 vi.setSystemTime(new Date(browserNow))
 await act(async () => { fireEvent.click(button) })
 const posts = requests.mock.calls.filter(([url, options]) => url.endsWith('/admin/backfills') && options?.method === 'POST')
 expect(posts).toHaveLength(1)
 expect(JSON.parse(posts[0][1]!.body as string)).toEqual(target)
 expect(requests.mock.calls.some(([url]) => url.endsWith('/admin/analyze'))).toBe(false)
})

it.each([
 ['missing', undefined], ['null', null], ['bad boundary', { start: 'invalid', end: window.end }],
 ['reversed', { start: window.end, end: window.start }], ['empty', { start: window.start, end: window.start }],
 ['wrong type', { start: 1, end: window.end }],
 ['missing timezone', { start: '2026-10-01T00:00:00', end: '2026-10-15T00:00:00' }],
 ['normalized February 30', { start: '2026-02-30T00:00:00Z', end: '2026-03-15T00:00:00Z' }],
 ['normalized hour 24', { start: '2026-10-01T24:00:00Z', end: '2026-10-15T00:00:00Z' }],
] as const)('blocks an unavailable target (%s) without posting and explains refresh', async (_label, target) => {
 const requests = showBackfillTarget({ ...base, requested_window: target, coverage_error: 'coverage_timezone_unavailable' })
 const button = await screen.findByRole('button', { name: '补采最近 14 天' })
 expect(button).toBeDisabled()
 expect(await screen.findByText(/目标窗口.*暂无法确认.*刷新采集显示/)).toBeInTheDocument()
 await act(async () => { fireEvent.click(button) })
 expect(requests.mock.calls.some(([url]) => url.endsWith('/admin/backfills'))).toBe(false)
})

it('explains the unchanged single-request limit for a completed-day DST window over fourteen elapsed days', async () => {
 const target = { start: '2026-10-25T04:00:00Z', end: '2026-11-08T05:00:00Z' }
 const requests = showBackfillTarget({ ...base, requested_window: target })
 const button = await screen.findByRole('button', { name: '补采最近 14 天' })
 expect(button).toBeDisabled()
 expect(await screen.findByText(/超过单次.*14.*24.*小时.*限制/)).toBeInTheDocument()
 await act(async () => { fireEvent.click(button) })
 expect(requests.mock.calls.some(([url]) => url.endsWith('/admin/backfills'))).toBe(false)
})

it('enables initial backfill only after refresh supplies a valid server target', async () => {
 const collection: Record<string, unknown> = { ...base, requested_window: null }
 const requests = showBackfillTarget(collection)
 const button = await screen.findByRole('button', { name: '补采最近 14 天' })
 expect(button).toBeDisabled()
 await screen.findByText(/目标窗口.*暂无法确认.*刷新采集显示/)
 const refreshed = { start: '2026-10-02T16:00:00Z', end: '2026-10-16T16:00:00Z' }
 collection.requested_window = refreshed
 await act(async () => { fireEvent.click(screen.getByRole('button', { name: '刷新采集显示' })) })
 expect(button).toBeEnabled()
 await act(async () => { fireEvent.click(button) })
 const posts = requests.mock.calls.filter(([url, options]) => url.endsWith('/admin/backfills') && options?.method === 'POST')
 expect(posts).toHaveLength(1)
 expect(JSON.parse(posts[0][1]!.body as string)).toEqual(refreshed)
})
it.each([
 { start: '2024-02-29T00:00:00Z', end: '2024-03-14T00:00:00Z' },
 { start: '2026-10-01T00:00:00.123456789+08:00', end: '2026-10-15T00:00:00.123456789+08:00' },
])('preserves valid leap-day or offset target bytes without normalization: $start', async target => {
 const requests = showBackfillTarget({ ...base, requested_window: target })
 const button = await screen.findByRole('button', { name: '补采最近 14 天' })
 await waitFor(() => expect(button).toBeEnabled())
 await act(async () => { fireEvent.click(button) })
 const posts = requests.mock.calls.filter(([url, options]) => url.endsWith('/admin/backfills') && options?.method === 'POST')
 expect(posts).toHaveLength(1)
 expect(JSON.parse(posts[0][1]!.body as string)).toEqual(target)
})