import { useEffect, useRef, useState } from 'react'
import { ApiError, errorText, requestConsole, type AnalysisRun, type BackfillRun, type RunAccepted, type RunStatus, type TimeWindow } from './api'
import { useAuth } from './auth'

export const terminalRun = (status?: RunStatus) => status === 'completed' || status === 'failed' || status === 'cancelled'
type Job = AnalysisRun | BackfillRun
interface State<T> { epoch: number; id: string; status?: RunStatus; data: T | null; error: string; pending: boolean; cancelRequested: boolean; uncertain: boolean }
export function useAnalysisRun<T extends Job>(kind: 'analysis' | 'backfill', initial?: T | null, records?: readonly T[]) {
 const auth = useAuth()
 const path = kind === 'analysis' ? '/admin/analysis-runs' : '/admin/backfills'
 const empty = (): State<T> => ({ epoch: auth.epoch, id: '', data: null, error: '', pending: false, cancelRequested: false, uncertain: false })
 const [state, setState] = useState<State<T>>(empty)
 const [attempt, setAttempt] = useState(0)
 const explicit = useRef(-1)
 // A failed start response may hide an accepted job. Only a subsequent explicit
 // collection refresh may reconcile it; stale pre-start records are not proof.
 const uncertainStart = useRef<{ epoch: number; startedAt: number; knownIds: Set<string>; previousId: string; window?: TimeWindow; refreshRequested: boolean; beforeRefresh?: readonly T[] } | null>(null)
 const busy = useRef(false)
 const local = useRef(new AbortController())
 useEffect(() => { local.current = new AbortController(); const active = local.current; return () => active.abort() }, [])
 const current = state.epoch === auth.epoch ? state : empty()
 useEffect(() => {
  if (initial && explicit.current !== auth.epoch) setState({ ...empty(), id: initial.id, status: initial.status, data: initial })
 }, [initial?.id, auth.epoch])
 useEffect(() => {
  const pending = uncertainStart.current
  if (!current.uncertain || !pending?.refreshRequested || pending.epoch !== auth.epoch || auth.signal.aborted || !auth.isCurrent(pending.epoch) || records === pending.beforeRefresh) return
  pending.refreshRequested = false
  const matches = (records ?? []).filter(job => {
   if (!job.id || !['queued', 'running', 'completed', 'failed', 'cancelled'].includes(job.status)) return false
   if (kind === 'analysis') return (job as AnalysisRun).source === 'manual' && !pending.knownIds.has(job.id) && Date.parse(job.created_at) >= pending.startedAt
   const backfill = job as BackfillRun
   if (!pending.window || Date.parse(backfill.requested_window.start) !== Date.parse(pending.window.start) || Date.parse(backfill.requested_window.end) !== Date.parse(pending.window.end)) return false
   return !pending.knownIds.has(job.id) && Date.parse(job.created_at) >= pending.startedAt ||
    job.id === pending.previousId && Date.parse(backfill.updated_at) >= pending.startedAt
  })
  if (matches.length === 1) {
   const job = matches[0]
   uncertainStart.current = null
   setState(old => old.epoch === pending.epoch && old.uncertain ? { ...empty(), id: job.id, status: job.status, data: job } : old)
  } else {
   setState(old => old.epoch === pending.epoch && old.uncertain ? { ...old, error: '启动结果仍未确认，暂未找到唯一匹配的任务。浏览器与服务的时间差异或多个候选任务可能阻止确认。已暂停重复启动，请继续刷新采集显示核对。' } : old)
  }
 }, [records, auth.epoch])
 const active = Boolean(current.id && !terminalRun(current.status))
 useEffect(() => {
  if (!active || !auth.identity) return
  let stopped = false
  let timer: ReturnType<typeof setTimeout> | undefined
  let pending: AbortController | undefined
  let step = 0
  const epoch = auth.epoch
  const id = current.id
  const expected = { userId: auth.identity.user.id, csrfToken: auth.identity.csrf_token }
  const clear = () => { clearTimeout(timer); pending?.abort() }
  const read = async () => {
   if (stopped || document.visibilityState === 'hidden' || auth.signal.aborted) return
   pending?.abort()
   const controller = new AbortController(); pending = controller
   const signal = AbortSignal.any([controller.signal, auth.signal, local.current.signal])
   let finished = false
   try {
    const data = await requestConsole<T>(path + '/' + encodeURIComponent(id), { signal, expected })
    if (signal.aborted || stopped || !auth.isCurrent(epoch)) return
    if (data.id !== id || !['queued', 'running', 'completed', 'failed', 'cancelled'].includes(data.status)) throw new ApiError(502, 'invalid_response')
    finished = terminalRun(data.status)
    setState(old => old.epoch === epoch && old.id === id ? { ...old, data, status: data.status, error: '' } : old)
   } catch (cause) {
    if (signal.aborted || stopped || !auth.isCurrent(epoch)) return
    if (cause instanceof ApiError && cause.status === 401) { auth.expire(); return }
    setState(old => ({ ...old, error: errorText(cause) }))
   }
   if (!finished && !stopped && !signal.aborted) timer = setTimeout(() => void read(), [2000, 5000, 10000][Math.min(step++, 2)])
  }
  const visibility = () => { clear(); if (document.visibilityState !== 'hidden') void read() }
  const abort = () => { stopped = true; clear() }
  document.addEventListener('visibilitychange', visibility)
  auth.signal.addEventListener('abort', abort, { once: true })
  void read()
  return () => { abort(); document.removeEventListener('visibilitychange', visibility); auth.signal.removeEventListener('abort', abort) }
 }, [current.id, active, auth.epoch, attempt, path])
 async function mutate(cancel: boolean, body: object = {}) {
  if (busy.current || !auth.identity || (!cancel && (active || current.uncertain)) || (cancel && !active)) return
  const epoch = auth.epoch
  explicit.current = epoch
  busy.current = true
  const previous = current
  if (!cancel) uncertainStart.current = {
   epoch, startedAt: Date.now(), knownIds: new Set([...(records ?? []).map(job => job.id), ...(initial ? [initial.id] : []), ...(previous.id ? [previous.id] : [])]),
   previousId: previous.id, window: kind === 'backfill' ? body as TimeWindow : undefined,
   refreshRequested: false,
  }
  setState(cancel ? { ...current, pending: true, error: '' } : { ...empty(), pending: true })
  const signal = AbortSignal.any([auth.signal, local.current.signal])
  try {
   const accepted = await requestConsole<RunAccepted>(cancel ? path + '/' + encodeURIComponent(previous.id) + '/cancel' : kind === 'analysis' ? '/admin/analyze' : path, {
    method: 'POST', body, signal, csrfToken: auth.identity.csrf_token,
    expected: { userId: auth.identity.user.id, csrfToken: auth.identity.csrf_token },
   })
   if (signal.aborted || !auth.isCurrent(epoch)) return
   if (!accepted.run_id || !['queued', 'running', 'completed', 'failed', 'cancelled'].includes(accepted.status)) throw new ApiError(502, 'invalid_response')
   if (cancel) setState(old => ({ ...old, pending: false, cancelRequested: true }))
   else { uncertainStart.current = null; setState({ ...empty(), id: accepted.run_id, status: 'queued' }) }
  } catch (cause) {
   if (signal.aborted || !auth.isCurrent(epoch)) return
   if (cause instanceof ApiError && cause.status === 401) auth.expire()
   else {
    const uncertain = !cancel && !(cause instanceof ApiError && [400, 403, 404, 409, 422].includes(cause.status))
    if (!uncertain && !cancel) uncertainStart.current = null
    setState(old => ({ ...old, pending: false, uncertain, error: errorText(cause) + (uncertain ? ' 启动结果尚未确认，已暂停重复启动；请刷新采集显示核对。' : '') }))
   }
  } finally { busy.current = false }
 }
 function refresh() {
  const pending = uncertainStart.current
  if (current.uncertain && pending?.epoch === auth.epoch) {
   pending.refreshRequested = true
   pending.beforeRefresh = records
  }
  setAttempt(n => n + 1)
 }
 return { ...current, active, start: (body: object) => mutate(false, body), cancel: () => mutate(true), refresh }
}
