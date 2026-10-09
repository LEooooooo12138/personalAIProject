import type { ConsoleMessage } from '../api'
import type { ControlProposal, DeviceResult } from './control-types'

export type ChatState = { epoch: number; revision: number; sid: string | null; messages: ConsoleMessage[]; proposals: Record<string, ControlProposal | null>; phase: 'idle' | 'loading' | 'sending' | 'unknown'; pending: string | null; error: string | null; recovered: boolean; recoveryError: string | null }
export type Action =
  | { type: 'identity'; epoch: number }
  | { type: 'select'; sid: string | null }
  | { type: 'history'; epoch: number; revision: number; sid: string; messages: ConsoleMessage[] }
  | { type: 'notFound'; epoch: number; revision: number; sid: string }
  | { type: 'failure'; epoch: number; revision: number; message: string }
  | { type: 'send'; content: string }
  | { type: 'serverSession'; epoch: number; revision: number; sid: string }
  | { type: 'response'; epoch: number; revision: number; content: string }
  | { type: 'proposal'; epoch: number; revision: number; sid: string; id?: string; proposal: ControlProposal | null }
  | { type: 'structured'; epoch: number; revision: number; sid: string; content?: string; proposal?: ControlProposal; device_result?: DeviceResult }
  | { type: 'unknown'; epoch: number; revision: number }
  | { type: 'checked'; epoch: number; revision: number }
  | { type: 'allowRetry' }
  | { type: 'continue' }

export function initialChatState(epoch: number): ChatState { return { epoch, revision: 0, sid: null, messages: [], proposals: {}, phase: 'idle', pending: null, error: null, recovered: false, recoveryError: null } }
export function transition(state: ChatState, action: Action): ChatState {
  if (action.type === 'identity') return initialChatState(action.epoch)
  if (action.type === 'select') return { ...initialChatState(state.epoch), revision: state.revision + 1, sid: action.sid, phase: action.sid ? 'loading' : 'idle' }
  if (action.type === 'allowRetry') return state.phase === 'unknown' && state.recovered ? { ...state, phase: 'idle', pending: null, error: null, recovered: false, recoveryError: null, messages: state.sid ? state.messages : [] } : state
  if (action.type === 'continue') return state.phase === 'unknown' && state.recovered && state.sid ? { ...state, phase: 'idle', pending: null, error: null, recovered: false, recoveryError: null } : state
  if (action.type === 'send') {
    if (state.phase !== 'idle' || !action.content.trim()) return state
    return { ...state, phase: 'sending', pending: action.content, error: null, recovered: false, recoveryError: null, messages: [...state.messages, { role: 'user', content: action.content, timestamp: new Date().toISOString() }] }
  }
  if (action.epoch !== state.epoch || action.revision !== state.revision) return state
  if (action.type === 'proposal') {
    if (action.sid !== state.sid || (action.proposal && action.proposal.session_id !== state.sid)) return state
    const id = action.proposal?.id ?? action.id
    return id ? { ...state, proposals: { ...state.proposals, [id]: action.proposal } } : state
  }
  if (action.type === 'structured') {
    if (state.phase !== 'sending' || action.sid !== state.sid || (action.proposal && action.proposal.session_id !== state.sid)) return state
    const message: ConsoleMessage = { role: 'assistant', content: action.content ?? '', timestamp: new Date().toISOString(), ...(action.proposal ? { attachments: [{ kind: 'control_proposal', proposal_id: action.proposal.id }] } : {}), ...(action.device_result ? { device_result: action.device_result } : {}) }
    return { ...state, phase: 'idle', pending: null, error: null, recovered: false, recoveryError: null, messages: [...state.messages, message], proposals: action.proposal ? { ...state.proposals, [action.proposal.id]: action.proposal } : state.proposals }
  }
  if (action.type === 'history') {
    if (action.sid !== state.sid) return state
    if (state.phase === 'unknown' && state.pending) {
      return { ...state, messages: action.messages, proposals: {}, recovered: true, recoveryError: null }
    }
    return { ...state, messages: action.messages, proposals: {}, phase: 'idle', error: null }
  }
  if (action.type === 'notFound') return action.sid === state.sid ? { ...initialChatState(state.epoch), revision: state.revision + 1, error: '该对话已不存在。' } : state
  if (action.type === 'failure') return state.phase === 'unknown' ? { ...state, recovered: false, recoveryError: action.message } : { ...state, phase: 'idle', error: action.message }
  if (action.type === 'checked') return state.phase === 'unknown' ? { ...state, recovered: true, recoveryError: null } : state
  if (action.type === 'serverSession') return action.sid ? { ...state, sid: action.sid } : state
  if (action.type === 'response') return state.phase === 'sending' ? { ...state, phase: 'idle', pending: null, error: null, recovered: false, recoveryError: null, messages: [...state.messages, { role: 'assistant', content: action.content, timestamp: new Date().toISOString() }] } : state
  if (action.type === 'unknown') return state.phase === 'sending' ? { ...state, phase: 'unknown', recovered: false, recoveryError: null, error: '发送结果尚不确定；上一条可能已送达，重发可能产生重复。' } : state
  return state
}
