import { describe, expect, it } from 'vitest'
import { initialChatState, transition, type ChatState } from './chat-state'

describe('chat state ownership', () => {
  it('ignores late history and 404 after a newer session is selected', () => {
    let state = initialChatState(7)
    state = transition(state, { type: 'select', sid: 'old' })
    const oldRevision = state.revision
    state = transition(state, { type: 'select', sid: 'new' })
    state = transition(state, { type: 'history', epoch: 7, revision: oldRevision, sid: 'old', messages: [{ role: 'assistant', content: '旧消息', timestamp: '2026-01-01T00:00:00Z' }] })
    state = transition(state, { type: 'notFound', epoch: 7, revision: oldRevision, sid: 'old' })
    expect(state.sid).toBe('new')
    expect(state.messages).toEqual([])
  })

  it('clears private content and rejects previous identity responses', () => {
    let state = initialChatState(4)
    state = transition(state, { type: 'select', sid: 'alice' })
    const oldRevision = state.revision
    state = transition(state, { type: 'identity', epoch: 5 })
    state = transition(state, { type: 'history', epoch: 4, revision: oldRevision, sid: 'alice', messages: [{ role: 'user', content: '秘密', timestamp: '2026-01-01T00:00:00Z' }] })
    expect(state).toMatchObject({ epoch: 5, sid: null, messages: [] })
  })

  it('keeps the SID on a general error and only clears it for matching not-found', () => {
    let state = transition(initialChatState(2), { type: 'select', sid: 'kept' })
    const revision = state.revision
    state = transition(state, { type: 'failure', epoch: 2, revision, message: '暂时无法读取' })
    expect(state.sid).toBe('kept')
    state = transition(state, { type: 'notFound', epoch: 2, revision, sid: 'other' })
    expect(state.sid).toBe('kept')
    state = transition(state, { type: 'notFound', epoch: 2, revision, sid: 'kept' })
    expect(state.sid).toBeNull()
  })

  it('does not mint a SID before the server session frame', () => {
    let state: ChatState = initialChatState(1)
    state = transition(state, { type: 'send', content: '你好' })
    expect(state.sid).toBeNull()
    state = transition(state, { type: 'serverSession', epoch: 1, revision: state.revision, sid: 'server-issued' })
    expect(state.sid).toBe('server-issued')
  })

  it('keeps an identical older question and answer inconclusive after recovery', () => {
    let state = transition(initialChatState(1), { type: 'select', sid: 'same' })
    state = transition(state, { type: 'history', epoch: 1, revision: state.revision, sid: 'same', messages: [
      { role: 'user', content: '你好', timestamp: '2026-01-01T00:00:00Z' },
      { role: 'assistant', content: '旧回答', timestamp: '2026-01-01T00:00:01Z' },
    ] })
    state = transition(state, { type: 'send', content: '你好' })
    state = transition(state, { type: 'unknown', epoch: 1, revision: state.revision })
    state = transition(state, { type: 'history', epoch: 1, revision: state.revision, sid: 'same', messages: [
      { role: 'user', content: '你好', timestamp: '2026-01-01T00:00:00Z' },
      { role: 'assistant', content: '旧回答', timestamp: '2026-01-01T00:00:01Z' },
    ] })
    expect(state.phase).toBe('unknown')
    expect(state.pending).toBe('你好')
    expect(state.recovered).toBe(true)
    expect(state.messages).toHaveLength(2)
    state = transition(state, { type: 'allowRetry' })
    expect(state.messages.map((message) => message.content)).toEqual(['你好', '旧回答'])
  })

  it('preserves the unknown warning and blocks decisions after history fails', () => {
    let state = transition(initialChatState(1), { type: 'select', sid: 'same' })
    state = transition(state, { type: 'history', epoch: 1, revision: state.revision, sid: 'same', messages: [] })
    state = transition(state, { type: 'send', content: '可能重复' })
    state = transition(state, { type: 'unknown', epoch: 1, revision: state.revision })
    state = transition(state, { type: 'failure', epoch: 1, revision: state.revision, message: '历史暂时无法读取' })
    expect(state).toMatchObject({ phase: 'unknown', recovered: false, recoveryError: '历史暂时无法读取' })
    expect(state.error).toContain('结果尚不确定')
    expect(transition(state, { type: 'allowRetry' }).phase).toBe('unknown')
    expect(transition(state, { type: 'continue' }).phase).toBe('unknown')
  })

  it('removes an unconfirmed local question before retrying a new chat with no server SID', () => {
    let state = transition(initialChatState(1), { type: 'send', content: '首次可能未保存' })
    state = transition(state, { type: 'unknown', epoch: 1, revision: state.revision })
    state = transition(state, { type: 'checked', epoch: 1, revision: state.revision })
    expect(state.messages.map((message) => message.content)).toEqual(['首次可能未保存'])
    state = transition(state, { type: 'allowRetry' })
    expect(state.messages).toEqual([])
    state = transition(state, { type: 'send', content: '首次可能未保存' })
    expect(state.messages.map((message) => message.content)).toEqual(['首次可能未保存'])
  })
})

it('keeps authoritative control references scoped to the current identity and session', () => {
  let state = transition(initialChatState(1), { type: 'select', sid: 'one' })
  state = transition(state, { type: 'history', epoch: 1, revision: state.revision, sid: 'one', messages: [] })
  const proposal = { id: 'p', session_id: 'one', request_id: 'r', entity_id: 'light.one', name: '灯', area_name: '客厅', action: 'turn_on' as const, status: 'pending' as const, created_at: '', expires_at: '2099-01-01' }
  state = transition(state, { type: 'proposal', epoch: 1, revision: state.revision, sid: 'other', proposal })
  expect(state.proposals).toEqual({})
  state = transition(state, { type: 'proposal', epoch: 1, revision: state.revision, sid: 'one', proposal })
  expect(state.proposals.p).toEqual(proposal)
  state = transition(state, { type: 'identity', epoch: 2 })
  expect(state.proposals).toEqual({})
})
