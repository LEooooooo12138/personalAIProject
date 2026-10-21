import { describe, expect, it } from 'vitest'
import type { ConsoleMessage } from '../api'
import { initialChatState, transition, type Action } from './chat-state'
import type { ControlProposal } from './control-types'

const proposal: ControlProposal = { id: 'p', session_id: 'one', request_id: 'r', entity_id: 'light.one', name: '灯', area_name: '客厅', action: 'turn_on', status: 'succeeded', created_at: '', expires_at: '' }
const stored = { role: 'assistant', content: '好呀，已读取到灯的目标状态。', timestamp: '2026-10-12T12:00:00Z', event_id: 'result-one', personality_revision: 'persona-one', presentation_version: 'v1', attachments: [{ kind: 'control_proposal' as const, proposal_id: 'p' }] }
function ready() { const state = transition(initialChatState(1), { type: 'select', sid: 'one' }); return transition(state, { type: 'history', epoch: 1, revision: state.revision, sid: 'one', messages: [] }) }
function result(overrides: Record<string, unknown> = {}): Action { return { type: 'proposal', epoch: 1, revision: 1, sid: 'one', id: 'p', proposal, assistant_message: { index: 0, message: stored }, ...overrides } as Action }

describe('persisted device assistant messages', () => {
  it('appends the stored body and metadata once at its authoritative index', () => {
    const next = transition(ready(), result())
    expect(next.messages).toEqual([stored])
    expect(transition(next, result()).messages).toEqual([stored])
  })
  it.each([{ epoch: 2 }, { revision: 2 }, { sid: 'other' }, { id: 'other' }, { proposal: { ...proposal, session_id: 'other' } }])('rejects stale or mismatched ownership %j', (change) => {
    expect(transition(ready(), result(change)).messages).toEqual([])
  })
  it.each([
    { index: -1, message: stored }, { index: 0.5, message: stored },
    { index: 0, message: { ...stored, event_id: '' } },
    { index: 0, message: { ...stored, role: 'user' } },
    { index: 0, message: { ...stored, attachments: [{ kind: 'control_proposal', proposal_id: 'other' }] } },
    { index: 0, message: { ...stored, device_result: { entity_id: 'light.other', name: '其他灯', state: 'on', observed_at: '' } } },
  ])('keeps the card independent of invalid language envelope %j', (assistant_message) => {
    const next = transition(ready(), result({ assistant_message }))
    expect(next.proposals.p).toEqual(proposal)
    expect(next.messages).toEqual([])
  })
  it('defers an index gap to authoritative history', () => {
    expect(transition(ready(), result({ assistant_message: { index: 2, message: stored } }))).toMatchObject({ messages: [], controlHistoryNeeded: true })
  })
  it('does not overwrite a pending chat or accept an overlapping history read', () => {
    const sending = transition(ready(), { type: 'send', content: '我的问题' })
    const next = transition(sending, result({ assistant_message: { index: 1, message: stored } }))
    expect(next).toMatchObject({ phase: 'sending', pending: '我的问题', controlHistoryNeeded: true })
    expect(next.messages).toEqual(sending.messages)
    expect(transition(next, { type: 'history', epoch: 1, revision: 1, sid: 'one', messages: [stored] })).toBe(next)
  })
  it('preserves the first text for the same event and requests history for conflicting indexes', () => {
    const next = transition(ready(), result())
    const conflict = transition(next, result({ assistant_message: { index: 1, message: { ...stored, content: '不可覆盖的改写' } } }))
    expect(conflict.messages).toEqual([stored])
    expect(conflict).toMatchObject({ controlHistoryNeeded: true })
  })
  it('recovers device facts and metadata from history without regenerating text', () => {
    const device = { entity_id: 'light.one', name: '灯', state: 'on', observed_at: '' }
    const message: ConsoleMessage = { ...stored, device_result: device }
    const pending = transition(ready(), result({ assistant_message: { index: 2, message: stored } }))
    const next = transition(pending, { type: 'history', epoch: 1, revision: 1, sid: 'one', messages: [message] })
    expect(next.messages).toEqual([message])
    expect(next).toMatchObject({ controlHistoryNeeded: false })
  })
  it('accepts legacy bare proposals and unavailable language without changing history', () => {
    expect(transition(ready(), result({ assistant_message: undefined, assistant_message_unavailable: true }))).toMatchObject({ proposals: { p: proposal }, messages: [] })
  })
  it('keeps WebSocket presentation metadata and facts without appending a repeated event', () => {
    const sending = transition(ready(), { type: 'send', content: '查询灯' })
    const device = { entity_id: 'light.one', name: '灯', state: 'on', observed_at: '' }
    const response = { type: 'structured', epoch: 1, revision: 1, sid: 'one', content: stored.content, event_id: 'query-one', personality_revision: 'persona-one', presentation_version: 'v1', device_result: device } as Action
    const next = transition(sending, response)
    expect(next.messages[1]).toMatchObject({ content: stored.content, event_id: 'query-one', personality_revision: 'persona-one', presentation_version: 'v1', device_result: device })
    const retry = transition(next, { type: 'send', content: '查询灯' })
    expect(transition(retry, response).messages.filter((message) => message.event_id === 'query-one')).toHaveLength(1)
  })
})
