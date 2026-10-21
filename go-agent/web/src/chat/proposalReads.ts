import { requestConsole } from '../api'

export type ProposalRead = (id: string, signal: AbortSignal) => Promise<unknown>
type Job = { id: string; signal: AbortSignal; resolve: (value: unknown) => void; reject: (cause: unknown) => void; abort: () => void }

// A page-local transport queue. Proposal state and response validation remain with callers.
export function createProposalReader(): ProposalRead {
  const queue: Job[] = []
  const active = new Set<string>()
  function pump() {
    while (active.size < 2) {
      const index = queue.findIndex((job) => !active.has(job.id))
      if (index < 0) return
      const [job] = queue.splice(index, 1)
      job.signal.removeEventListener('abort', job.abort)
      if (job.signal.aborted) { job.reject(new DOMException('Aborted', 'AbortError')); continue }
      active.add(job.id)
      // Aborting a running request does not release its slot until transport settles.
      void requestConsole<unknown>('/control/proposals/' + encodeURIComponent(job.id), { signal: job.signal })
        .then(job.resolve, job.reject)
        .finally(() => { active.delete(job.id); pump() })
    }
  }
  return (id, signal) => new Promise((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException('Aborted', 'AbortError')); return }
    const job: Job = { id, signal, resolve, reject, abort: () => {
      const index = queue.indexOf(job)
      if (index >= 0) { queue.splice(index, 1); reject(new DOMException('Aborted', 'AbortError')) }
    } }
    queue.push(job)
    signal.addEventListener('abort', job.abort, { once: true })
    pump()
  })
}
