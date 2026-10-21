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

it('clears a failed turn without deleting prior history and rolls back only an unsent optimistic message', () => {
  let state = transition(initialChatState(1), { type: 'select', sid: 'one' })
  state = transition(state, { type: 'history', epoch: 1, revision: state.revision, sid: 'one', messages: [{ role: 'assistant', content: '旧回答', timestamp: '2026-01-01' }] })
  state = transition(state, { type: 'send', content: '新问题' })
  state = transition(state, { type: 'sendFailed', epoch: 1, revision: state.revision, message: '未发送', unsent: true })
  expect(state).toMatchObject({ phase: 'idle', pending: null, sid: 'one', error: '未发送' })
  expect(state.messages.map((message) => message.content)).toEqual(['旧回答'])
  state = transition(state, { type: 'send', content: '新问题' })
  state = transition(state, { type: 'sendFailed', epoch: 1, revision: state.revision, message: '认证失败' })
  expect(state).toMatchObject({ phase: 'idle', pending: null, error: '认证失败' })
  expect(state.messages.map((message) => message.content)).toEqual(['旧回答', '新问题'])
})


it('rollover_preserves_only_pending_turn and its original timestamp', () => {
  let state = transition(initialChatState(3), { type: 'select', sid: 'old' })
  state = transition(state, { type: 'history', epoch: 3, revision: state.revision, sid: 'old', messages: [{ role: 'user', content: '旧问题', timestamp: 'old' }, { role: 'assistant', content: '旧回答', timestamp: 'old' }] })
  state = transition(state, { type: 'send', content: '本轮问题' })
  const pending = state.messages.at(-1)!
  const revision = state.revision
  state = { ...state, proposals: { old: null } }
  state = transition(state, { type: 'serverSession', epoch: 3, revision, sid: 'new' })
  expect(state.messages).toEqual([pending])
  expect(state.proposals).toEqual({})
  expect(state).toMatchObject({ revision, phase: 'sending', pending: '本轮问题', sid: 'new' })
  state = transition(state, { type: 'response', epoch: 3, revision, content: '本轮回答' })
  expect(state.messages.map((message) => message.content)).toEqual(['本轮问题', '本轮回答'])
})
it('accepts first SID and identical SID while ignoring unsolicited or stale SID changes', () => {
  const idle = initialChatState(4)
  expect(transition(idle, { type: 'serverSession', epoch: 4, revision: 0, sid: 'unsolicited' })).toBe(idle)
  const sending = transition(idle, { type: 'send', content: '首条' })
  const assigned = transition(sending, { type: 'serverSession', epoch: 4, revision: 0, sid: 'first' })
  expect(assigned.messages).toEqual(sending.messages)
  expect(transition(assigned, { type: 'serverSession', epoch: 4, revision: 0, sid: 'first' })).toBe(assigned)
  expect(transition(assigned, { type: 'serverSession', epoch: 3, revision: 0, sid: 'old-epoch' })).toBe(assigned)
  expect(transition(assigned, { type: 'serverSession', epoch: 4, revision: 1, sid: 'old-revision' })).toBe(assigned)
  const answered = transition(assigned, { type: 'response', epoch: 4, revision: 0, content: '回答' })
  expect(transition(answered, { type: 'serverSession', epoch: 4, revision: 0, sid: 'other' })).toBe(answered)
})


describe('server progress ownership', () => {
  function sending() {
    let state = transition(initialChatState(7), { type: 'send', content: 'question', requestId: 'r-one' })
    state = transition(state, { type: 'serverSession', epoch: 7, revision: 0, sid: 'one' })
    return state
  }
  const stage = { type: 'stage' as const, epoch: 7, revision: 0, sid: 'one', requestId: 'r-one', stage: 'generating' as const }

  it('accepts only a sending turn with matching epoch, revision, session and request', () => {
    const state = sending()
    expect(transition(state, stage).stage).toBe('generating')
    for (const mismatch of [{ epoch: 6 }, { revision: 1 }, { sid: 'other' }, { requestId: 'old' }]) {
      expect(transition(state, { ...stage, ...mismatch })).toEqual(state)
    }
    expect(transition(initialChatState(7), stage)).toEqual(initialChatState(7))
    let legacy = transition(initialChatState(7), { type: 'send', content: 'legacy' })
    legacy = transition(legacy, { type: 'serverSession', epoch: 7, revision: 0, sid: 'one' })
    expect(transition(legacy, { ...stage, requestId: '' })).toEqual(legacy)
    expect(transition(legacy, stage)).toEqual(legacy)
  })

  it('does not render again for an unchanged server stage', () => {
    const state = transition(sending(), stage)
    expect(transition(state, stage)).toBe(state)
  })

  it('clears the previous session stage at rollover and keeps the request identity', () => {
    const state = transition(transition(sending(), stage), { type: 'serverSession', epoch: 7, revision: 0, sid: 'rolled' })
    expect(state).toMatchObject({ phase: 'sending', sid: 'rolled', pendingRequestId: 'r-one', stage: null })
    expect(transition(state, stage)).toEqual(state)
    expect(transition(state, { ...stage, sid: 'rolled', stage: 'saving' }).stage).toBe('saving')
  })

  it.each(['response', 'sendFailed', 'unknown', 'identity', 'select', 'failure', 'structured'] as const)('clears progress on %s and ignores delayed stages', (type) => {
    const active = transition(sending(), stage)
    const actions = {
      response: { type: 'response' as const, epoch: 7, revision: 0, content: 'answer' },
      sendFailed: { type: 'sendFailed' as const, epoch: 7, revision: 0, message: 'failed' },
      unknown: { type: 'unknown' as const, epoch: 7, revision: 0 },
      identity: { type: 'identity' as const, epoch: 8 },
      select: { type: 'select' as const, sid: null },
      failure: { type: 'failure' as const, epoch: 7, revision: 0, message: 'failed' },
      structured: { type: 'structured' as const, epoch: 7, revision: 0, sid: 'one', content: 'device answer' },
    }
    const done = transition(active, actions[type])
    expect(done.stage).toBeNull()
    expect(done.pendingRequestId).toBeNull()
    expect(transition(done, stage)).toEqual(done)
  })
})
