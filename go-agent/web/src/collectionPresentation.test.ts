import { expect, it } from 'vitest'
import type { CollectionView } from './api'
import { collectionPresentation } from './collectionPresentation'
const empty: CollectionView = { phase: 'idle', last_attempt_at: null, last_success_at: null, last_failure_at: null, error_code: null, snapshot_at: null, window_start: null, window_end: null, completed_batches: 0, total_batches: 0, checkpoint: '2026-10-10T08:00:00Z' }
it('checkpoint_and_zero_batches_do_not_claim_a_complete_collection', () => {
  const view = collectionPresentation(empty)
  expect(view.progressKnown).toBe(false)
  expect(view.currentSnapshot).toBe(false)
  expect(view.steps.some((step) => step.state === 'complete')).toBe(false)
})
it('only_a_snapshot_from_the_current_attempt_counts_as_complete', () => {
  const old = { ...empty, phase: 'history' as const, last_attempt_at: '2026-10-10T08:00:00Z', snapshot_at: '2026-10-09T08:00:00Z' }
  expect(collectionPresentation(old).steps[0].state).toBe('waiting')
  const current = collectionPresentation({ ...old, snapshot_at: '2026-10-10T08:00:01Z', completed_batches: 1, total_batches: 3 })
  expect(current.currentSnapshot).toBe(true)
  expect(current.steps.map((step) => step.state)).toEqual(['complete', 'active', 'waiting'])
  expect(current.progressKnown).toBe(true)
})
it('preserves_a_saved_snapshot_when_history_fails_without_claiming_success', () => {
  const result = collectionPresentation({ ...empty, phase: 'failed', last_attempt_at: '2026-10-10T08:00:00Z', snapshot_at: '2026-10-10T08:00:01Z' })
  expect(result.title).toBe('历史补采失败')
  expect(result.steps.map((step) => step.state)).toEqual(['complete', 'failed', 'failed'])
})
it.each([-1, 1.2, NaN, Infinity, 4])('does_not_render_invalid_progress_for_completed=%s', (completed) => {
  expect(collectionPresentation({ ...empty, completed_batches: completed, total_batches: 3 }).progressKnown).toBe(false)
})
it('invalid_timestamps_cannot_complete_the_current_attempt', () => {
  const result = collectionPresentation({ ...empty, last_attempt_at: 'bad', snapshot_at: 'bad', last_success_at: 'bad' })
  expect(result.currentSnapshot).toBe(false)
  expect(result.steps.some((step) => step.state === 'complete')).toBe(false)
})
