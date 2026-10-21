import type { CollectionView } from './api'
import type { StepItem } from './ui'
function timestamp(value: string | null) { const parsed = value ? Date.parse(value) : NaN; return Number.isFinite(parsed) ? parsed : null }
export function hasCollectionTimestamp(value: string | null) { return timestamp(value) !== null }
export function collectionPresentation(data: CollectionView): { title: string; steps: StepItem[]; progressKnown: boolean; currentSnapshot: boolean } {
  const attempt = timestamp(data.last_attempt_at), snapshot = timestamp(data.snapshot_at), success = timestamp(data.last_success_at)
  const currentSnapshot = attempt !== null && snapshot !== null && snapshot >= attempt
  const complete = attempt !== null && success !== null && success >= attempt && data.phase === 'idle'
  const progressKnown = Number.isInteger(data.completed_batches) && Number.isInteger(data.total_batches) && data.total_batches > 0 && data.completed_batches >= 0 && data.completed_batches <= data.total_batches
  const title = data.phase === 'snapshot' ? '正在保存状态快照' : data.phase === 'history' ? '正在补采历史' : data.phase === 'failed' ? currentSnapshot ? '历史补采失败' : '采集失败' : '当前空闲'
  const steps: StepItem[] = [
    { id: 'snapshot', label: '状态快照', state: complete || currentSnapshot ? 'complete' : data.phase === 'snapshot' ? 'active' : data.phase === 'failed' ? 'failed' : 'waiting' },
    { id: 'history', label: '补采历史', state: complete ? 'complete' : data.phase === 'history' ? 'active' : data.phase === 'failed' && currentSnapshot ? 'failed' : 'waiting' },
    { id: 'complete', label: '完整采集', state: complete ? 'complete' : data.phase === 'failed' ? 'failed' : 'waiting' },
  ]
  return { title, steps, progressKnown, currentSnapshot }
}
