import { describe, expect, it } from 'vitest'
import { parseChatStageFrame } from './chat-stage'

describe('server chat stage protocol', () => {
  it.each(['queued', 'preparing', 'classifying', 'generating', 'saving'])('accepts the complete %s frame', (stage) => {
    expect(parseChatStageFrame({ type: 'chat_stage', session_id: 'server-one', request_id: 'request-one', stage })).toEqual({
      type: 'chat_stage', session_id: 'server-one', request_id: 'request-one', stage,
    })
  })

  it.each([
    null, [], 'generating', { type: 'chat_stage', stage: 'generating' },
    { type: 'chat_stage', session_id: 'one', stage: 'generating' },
    { type: 'chat_stage', request_id: 'r', stage: 'generating' },
    { type: 'response', session_id: 'one', request_id: 'r', stage: 'generating' },
    { type: 'chat_stage', session_id: '', request_id: 'r', stage: 'generating' },
    { type: 'chat_stage', session_id: 'one', request_id: '  ', stage: 'generating' },
    { type: 'chat_stage', session_id: 1, request_id: 'r', stage: 'generating' },
    { type: 'chat_stage', session_id: 'one', request_id: 1, stage: 'generating' },
    { type: 'chat_stage', session_id: 'one', request_id: 'r' },
    { type: 'chat_stage', session_id: 'one', request_id: 'r', stage: 'done' },
    { type: 'chat_stage', session_id: 'one', request_id: 'r', stage: 1 },
  ])('rejects malformed or incomplete frames: %j', (frame) => {
    expect(parseChatStageFrame(frame)).toBeNull()
  })
})
