import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useProposalRefresh } from './useProposalRefresh'
import type { ControlProposal } from './control-types'
vi.mock('../coordinator', () => ({ coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options) }))
const proposal = (id = 'p'): ControlProposal => ({ id, session_id: 'one', request_id: 'r', entity_id: 'switch.one', name: '开关', area_name: '客厅', action: 'turn_on', status: 'executing', created_at: '', expires_at: '2099-01-01' })
const options = (proposals: Record<string, ControlProposal | null> = { p: proposal() }) => ({ epoch: 1, revision: 1, sid: 'one', proposals, signal: new AbortController().signal, isCurrent: () => true, onProposal: vi.fn(), onUnauthorized: vi.fn(), onUnavailable: vi.fn() })
const flush = async () => { await act(async () => { await Promise.resolve() }) }
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(0); vi.stubGlobal('fetch', vi.fn(async () => Response.json(proposal()))) })
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
it('stops_at_sixty_seconds and lets an explicit GET restart the watch', async () => {
 const input = options(); const view = renderHook(() => useProposalRefresh(input)); await flush()
 await act(async () => { await vi.advanceTimersByTimeAsync(60000) })
 expect(view.result.current.staleIds.has('p')).toBe(true)
 const calls = vi.mocked(fetch).mock.calls.length
 await act(async () => { await vi.advanceTimersByTimeAsync(60000) })
 expect(vi.mocked(fetch)).toHaveBeenCalledTimes(calls)
 await act(async () => { await view.result.current.refresh('p') })
 expect(view.result.current.staleIds.has('p')).toBe(false)
 expect(vi.mocked(fetch)).toHaveBeenCalledTimes(calls + 1)
 expect(vi.mocked(fetch).mock.calls.every(([, request]) => request?.method === 'GET')).toBe(true)
})
it('hidden_pauses_and_visible_reads immediately', async () => {
 const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
 const input = options(); renderHook(() => useProposalRefresh(input)); await flush()
 visibility.mockReturnValue('hidden'); act(() => document.dispatchEvent(new Event('visibilitychange')))
 await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
 expect(fetch).toHaveBeenCalledTimes(1)
 visibility.mockReturnValue('visible'); act(() => document.dispatchEvent(new Event('visibilitychange'))); await flush()
 expect(fetch).toHaveBeenCalledTimes(2)
})
it('duplicate_attachment_reads_once and unknown_manual_reconcile_only', async () => {
 const input = options(); const view = renderHook(({ value }) => useProposalRefresh(value), { initialProps: { value: input } }); await flush()
 view.rerender({ value: { ...input, proposals: { p: proposal() } } }); await flush()
 expect(fetch).toHaveBeenCalledTimes(1)
 view.rerender({ value: { ...input, proposals: { p: { ...proposal(), status: 'unknown' } } } })
 await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
 expect(fetch).toHaveBeenCalledTimes(1)
})
it('two_request_limit queues a third distinct proposal without overlapping an ID', async () => {
 const releases: ((value: Response) => void)[] = []
 vi.mocked(fetch).mockImplementation(() => new Promise((resolve) => releases.push(resolve)))
 const input = options({ p: proposal(), q: proposal('q'), r: proposal('r') }); renderHook(() => useProposalRefresh(input)); await flush()
 expect(fetch).toHaveBeenCalledTimes(2)
 await act(async () => releases[0](Response.json({ ...proposal(), status: 'succeeded' })))
 expect(fetch).toHaveBeenCalledTimes(3)
 const urls = vi.mocked(fetch).mock.calls.map(([url]) => String(url))
 expect(new Set(urls).size).toBe(3)
})
it('old_sid_response_ignored after switching scope or unmounting', async () => {
 let release!: (response: Response) => void
 vi.mocked(fetch).mockImplementationOnce(() => new Promise((resolve) => { release = resolve }))
 const input = options(); const view = renderHook(({ value }) => useProposalRefresh(value), { initialProps: { value: input } }); await flush()
 const request = vi.mocked(fetch).mock.calls[0][1]
 view.rerender({ value: { ...input, sid: 'two', proposals: {} } })
 expect(request?.signal?.aborted).toBe(true)
 await act(async () => release(Response.json({ ...proposal(), status: 'succeeded' })))
 expect(input.onProposal).not.toHaveBeenCalled()
})
it.each([401, 404, 503])('status %s stops automatic reads and preserves safe recovery semantics', async (status) => {
 vi.mocked(fetch).mockResolvedValue(Response.json({ error: { code: status === 401 ? 'unauthenticated' : status === 404 ? 'not_found' : 'unavailable' } }, { status }))
 const input = options(); const view = renderHook(() => useProposalRefresh(input)); await flush()
 await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
 expect(fetch).toHaveBeenCalledTimes(1)
 expect(input.onProposal).not.toHaveBeenCalled()
 if (status === 401) expect(input.onUnauthorized).toHaveBeenCalledOnce()
 else expect(view.result.current.errors.p).toBeTruthy()
 if (status === 404) expect(input.onUnavailable).toHaveBeenCalledExactlyOnceWith('p')
})
it('rejects a returned proposal from another session without publishing it', async () => {
 vi.mocked(fetch).mockResolvedValue(Response.json({ ...proposal(), session_id: 'other' }))
 const input = options(); const view = renderHook(() => useProposalRefresh(input)); await flush()
 expect(input.onProposal).not.toHaveBeenCalled()
 expect(view.result.current.errors.p).toMatch(/响应格式异常/)
})
