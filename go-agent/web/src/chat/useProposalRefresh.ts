import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorText, requestConsole } from '../api'
import type { ProposalRead } from './proposalReads'
import { isControlProposal, type ControlProposal } from './control-types'

type Options = {
  epoch: number; revision: number; sid: string | null
  proposals: Record<string, ControlProposal | null>; signal: AbortSignal
  isCurrent: () => boolean; onProposal: (proposal: ControlProposal) => void
  onUnauthorized: () => void; onUnavailable?: (id: string) => void; readProposal?: ProposalRead
}
type Watch = { started: number; stopped: boolean; queued: boolean; running: boolean; timer?: ReturnType<typeof setTimeout>; deadline?: ReturnType<typeof setTimeout>; controller?: AbortController; waiters: (() => void)[]; resume?: boolean }
const duration = 60000
export function useProposalRefresh(options: Options) {
  const latest = useRef(options)
  latest.current = options
  const [staleIds, setStaleIds] = useState<ReadonlySet<string>>(new Set())
  const [errors, setErrors] = useState<Readonly<Record<string, string>>>({})
  const runtime = useRef<{ sync: () => void; refresh: (id: string) => Promise<void> } | null>(null)
  useEffect(() => {
    const { epoch, revision, sid, signal } = options
    let alive = true
    let running = 0
    const watches = new Map<string, Watch>()
    const queue: string[] = []
    const current = () => alive && !signal.aborted && latest.current.epoch === epoch && latest.current.revision === revision && latest.current.sid === sid && latest.current.isCurrent()
    const visible = () => document.visibilityState !== 'hidden'
    const finishWaiters = (watch: Watch) => { watch.waiters.splice(0).forEach((resolve) => resolve()) }
    const stop = (watch: Watch) => { watch.stopped = true; watch.queued = false; clearTimeout(watch.timer); clearTimeout(watch.deadline); watch.controller?.abort(); finishWaiters(watch) }
    const clearNotice = (id: string) => {
      setStaleIds((old) => { const next = new Set(old); next.delete(id); return next })
      setErrors((old) => { const next = { ...old }; delete next[id]; return next })
    }
    const stale = (id: string, watch: Watch) => {
      stop(watch)
      if (current()) setStaleIds((old) => new Set(old).add(id))
    }
    const begin = (id: string, watch: Watch) => {
      watch.started = Date.now(); watch.stopped = false; clearNotice(id)
      clearTimeout(watch.deadline)
      watch.deadline = setTimeout(() => stale(id, watch), duration)
    }
    const enqueue = (id: string, watch: Watch) => {
      if (!current() || !visible() || watch.stopped || watch.queued || watch.running) return
      watch.queued = true; queue.push(id); pump()
    }
    const schedule = (id: string, watch: Watch) => {
      if (!current() || !visible() || watch.stopped) return
      const elapsed = Date.now() - watch.started
      if (elapsed >= duration) { stale(id, watch); return }
      watch.timer = setTimeout(() => enqueue(id, watch), elapsed < 15000 ? 1000 : 3000)
    }
    async function read(id: string, watch: Watch) {
      watch.queued = false; watch.running = true; running++
      const controller = new AbortController(); watch.controller = controller
      try {
        const readSignal = AbortSignal.any([signal, controller.signal])
        const value = await (latest.current.readProposal ? latest.current.readProposal(id, readSignal) : requestConsole<unknown>('/control/proposals/' + encodeURIComponent(id), { signal: readSignal }))
        if (!current() || controller.signal.aborted || !visible()) return
        if (!isControlProposal(value) || value.id !== id || value.session_id !== sid) throw new ApiError(502, 'invalid_response')
        latest.current.onProposal(value)
        if (value.status !== 'executing') { stop(watch); clearNotice(id) }
      } catch (cause) {
        if (!current() || controller.signal.aborted || !visible()) return
        stop(watch)
        if (cause instanceof ApiError && cause.status === 401) latest.current.onUnauthorized()
        else {
          setErrors((old) => ({ ...old, [id]: errorText(cause) }))
          if (cause instanceof ApiError && cause.status === 404) latest.current.onUnavailable?.(id)
        }
      } finally {
        watch.running = false; watch.controller = undefined; running--; finishWaiters(watch)
        if (current() && visible() && !watch.stopped) {
          if (watch.resume) { watch.resume = false; enqueue(id, watch) }
          else schedule(id, watch)
        }
        pump()
      }
    }
    function pump() {
      if (!current() || !visible()) return
      while (running < 2 && queue.length) {
        const id = queue.shift()!
        const watch = watches.get(id)
        if (watch?.queued && !watch.stopped && !watch.running) void read(id, watch)
      }
    }
    function sync() {
      if (!current()) return
      for (const [id, watch] of watches) {
        if (latest.current.proposals[id]?.status !== 'executing') { stop(watch); watches.delete(id) }
      }
      if (!visible()) return
      for (const [id, proposal] of Object.entries(latest.current.proposals)) {
        if (!proposal || proposal.session_id !== sid || proposal.status !== 'executing' || watches.has(id)) continue
        const watch: Watch = { started: 0, stopped: false, queued: false, running: false, waiters: [] }
        watches.set(id, watch); begin(id, watch); enqueue(id, watch)
      }
    }
    async function refresh(id: string) {
      const proposal = latest.current.proposals[id]
      if (!current() || !visible() || !proposal || proposal.session_id !== sid) return
      let watch = watches.get(id)
      if (!watch) { watch = { started: 0, stopped: false, queued: false, running: false, waiters: [] }; watches.set(id, watch) }
      const done = new Promise<void>((resolve) => watch!.waiters.push(resolve))
      if (!watch.queued) { clearTimeout(watch.timer); begin(id, watch); if (watch.running) watch.resume = true; else enqueue(id, watch) }
      await done
    }
    const visibility = () => {
      queue.splice(0)
      for (const watch of watches.values()) stop(watch)
      if (visible()) {
        for (const [id, watch] of watches) {
          if (latest.current.proposals[id]?.status === 'executing') { begin(id, watch); if (watch.running) watch.resume = true; else enqueue(id, watch) }
        }
        sync()
      }
    }
    const abort = () => { alive = false; queue.splice(0); watches.forEach(stop) }
    setStaleIds(new Set()); setErrors({})
    runtime.current = { sync, refresh }
    signal.addEventListener('abort', abort, { once: true })
    document.addEventListener('visibilitychange', visibility)
    sync()
    return () => { abort(); runtime.current = null; signal.removeEventListener('abort', abort); document.removeEventListener('visibilitychange', visibility) }
  }, [options.epoch, options.revision, options.sid, options.signal])
  useEffect(() => { runtime.current?.sync() }, [options.proposals])
  const refresh = useCallback((id: string) => runtime.current?.refresh(id) ?? Promise.resolve(), [])
  return { refresh, staleIds, errors }
}
