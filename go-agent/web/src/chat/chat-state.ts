import type { ConsoleMessage, StoredAssistantMessage } from '../api'
import { isDeviceResult, type ChatStage, type ControlProposal, type DeviceResult, type MessagePresentation } from './control-types'

export type ChatState = { epoch: number; revision: number; sid: string | null; messages: ConsoleMessage[]; proposals: Record<string, ControlProposal | null>; phase: 'idle' | 'loading' | 'sending' | 'unknown'; pending: string | null; pendingRequestId: string | null; connected: boolean; stage: ChatStage | null; error: string | null; recovered: boolean; recoveryError: string | null; controlHistoryNeeded: boolean }
export type Action =
  | { type: 'identity'; epoch: number }
  | { type: 'select'; sid: string | null }
  | { type: 'history'; epoch: number; revision: number; sid: string; messages: ConsoleMessage[] }
  | { type: 'notFound'; epoch: number; revision: number; sid: string }
  | { type: 'failure'; epoch: number; revision: number; message: string }
  | { type: 'send'; content: string; requestId?: string }
  | { type: 'connected'; epoch: number; revision: number; requestId: string }
  | { type: 'stage'; epoch: number; revision: number; sid: string; requestId: string; stage: ChatStage }
  | { type: 'sendFailed'; epoch: number; revision: number; message: string; unsent?: boolean }
  | { type: 'serverSession'; epoch: number; revision: number; sid: string }
  | { type: 'controlInterleaved'; epoch: number; revision: number; sid: string }
  | ({ type: 'response'; epoch: number; revision: number; content: string; device_result?: DeviceResult } & MessagePresentation)
  | { type: 'proposal'; epoch: number; revision: number; sid: string; id?: string; proposal: ControlProposal | null; assistant_message?: unknown; assistant_message_unavailable?: boolean }
  | ({ type: 'structured'; epoch: number; revision: number; sid: string; content?: string; proposal?: ControlProposal; device_result?: DeviceResult } & MessagePresentation)
  | { type: 'unknown'; epoch: number; revision: number; message?: string }
  | { type: 'checked'; epoch: number; revision: number }
  | { type: 'allowRetry' }
  | { type: 'continue' }

export function initialChatState(epoch: number): ChatState { return { epoch, revision: 0, sid: null, messages: [], proposals: {}, phase: 'idle', pending: null, pendingRequestId: null, connected: false, stage: null, error: null, recovered: false, recoveryError: null, controlHistoryNeeded: false } }
export function messagePresentation(value: MessagePresentation): MessagePresentation {
  return Object.fromEntries(['event_id', 'personality_revision', 'presentation_version'].flatMap((key) => {
    const field = value[key as keyof MessagePresentation]
    return typeof field === 'string' && field.trim() ? [[key, field]] : []
  }))
}
export function storedControlMessage(value: unknown, proposal: ControlProposal): StoredAssistantMessage | null {
  if (!value || typeof value !== 'object') return null
  const { index, message } = value as Record<string, unknown>
  if (!Number.isSafeInteger(index) || (index as number) < 0 || !message || typeof message !== 'object') return null
  const item = message as Record<string, unknown>
  if (item.role !== 'assistant' || typeof item.content !== 'string' || !item.content.trim() || typeof item.timestamp !== 'string'
    || typeof item.event_id !== 'string' || !item.event_id.trim() || item.event_id.length > 512
    || ['personality_revision', 'presentation_version'].some((key) => item[key] !== undefined && typeof item[key] !== 'string')) return null
  if (!Array.isArray(item.attachments) || !item.attachments.length || !item.attachments.every((attachment) => attachment?.kind === 'control_proposal' && attachment.proposal_id === proposal.id)) return null
  if (item.device_result !== undefined && (!isDeviceResult(item.device_result) || item.device_result.entity_id !== proposal.entity_id)) return null
  return { index: index as number, message: message as ConsoleMessage }
}
function finishMessage(state: ChatState, message: ConsoleMessage, proposals = state.proposals): ChatState {
  const duplicate = !!message.event_id && state.messages.some((old) => old.event_id === message.event_id)
  return { ...state, phase: 'idle', pending: null, pendingRequestId: null, connected: false, stage: null, error: null, recovered: false, recoveryError: null, proposals,
    messages: duplicate ? state.messages : [...state.messages, message], controlHistoryNeeded: state.controlHistoryNeeded || duplicate }
}
export function transition(state: ChatState, action: Action): ChatState {
  if (action.type === 'identity') return initialChatState(action.epoch)
  if (action.type === 'select') return { ...initialChatState(state.epoch), revision: state.revision + 1, sid: action.sid, phase: action.sid ? 'loading' : 'idle' }
  if (action.type === 'allowRetry') return state.phase === 'unknown' && state.recovered ? { ...state, phase: 'idle', pending: null, pendingRequestId: null, connected: false, stage: null, error: null, recovered: false, recoveryError: null, messages: state.sid ? state.messages : [] } : state
  if (action.type === 'continue') return state.phase === 'unknown' && state.recovered && state.sid ? { ...state, phase: 'idle', pending: null, pendingRequestId: null, connected: false, stage: null, error: null, recovered: false, recoveryError: null } : state
  if (action.type === 'send') {
    if (state.phase !== 'idle' || !action.content.trim()) return state
    return { ...state, phase: 'sending', pending: action.content, pendingRequestId: action.requestId?.trim() ? action.requestId : null, connected: false, stage: null, error: null, recovered: false, recoveryError: null, messages: [...state.messages, { role: 'user', content: action.content, timestamp: new Date().toISOString() }] }
  }
  if (action.epoch !== state.epoch || action.revision !== state.revision) return state
  if (action.type === 'connected') return state.phase === 'sending' && !!state.pendingRequestId && action.requestId === state.pendingRequestId && !state.connected ? { ...state, connected: true } : state
  if (action.type === 'stage') {
    if (state.phase !== 'sending' || !state.pendingRequestId || action.sid !== state.sid || action.requestId !== state.pendingRequestId || action.stage === state.stage) return state
    return { ...state, stage: action.stage }
  }
  if (action.type === 'controlInterleaved') return action.sid === state.sid ? { ...state, controlHistoryNeeded: true } : state
  if (action.type === 'proposal') {
    if (action.sid !== state.sid || (action.proposal && action.proposal.session_id !== state.sid)) return state
    if (action.id && action.proposal && action.id !== action.proposal.id) return state
    const id = action.proposal?.id ?? action.id
    if (!id) return state
    const next = { ...state, proposals: { ...state.proposals, [id]: action.proposal } }
    const stored = action.proposal ? storedControlMessage(action.assistant_message, action.proposal) : null
    if (!stored) return next
    const existing = state.messages.findIndex((message) => message.event_id === stored.message.event_id)
    if (existing >= 0) return existing === stored.index && state.messages[existing].content === stored.message.content ? next : { ...next, controlHistoryNeeded: true }
    if (state.phase !== 'idle' || state.controlHistoryNeeded || stored.index !== state.messages.length) return { ...next, controlHistoryNeeded: true }
    return { ...next, messages: [...state.messages, stored.message] }
  }
  if (action.type === 'structured') {
    if (state.phase !== 'sending' || action.sid !== state.sid || (action.proposal && action.proposal.session_id !== state.sid)) return state
    const message: ConsoleMessage = { role: 'assistant', content: action.content ?? '', timestamp: new Date().toISOString(), ...messagePresentation(action), ...(action.proposal ? { attachments: [{ kind: 'control_proposal', proposal_id: action.proposal.id }] } : {}), ...(action.device_result ? { device_result: action.device_result } : {}) }
    return finishMessage(state, message, action.proposal ? { ...state.proposals, [action.proposal.id]: action.proposal } : state.proposals)
  }
  if (action.type === 'history') {
    if (action.sid !== state.sid || state.phase === 'sending') return state
    if (state.phase === 'unknown' && state.pending) {
      return { ...state, messages: action.messages, proposals: {}, recovered: true, recoveryError: null, controlHistoryNeeded: false }
    }
    return { ...state, messages: action.messages, proposals: {}, phase: 'idle', error: null, controlHistoryNeeded: false }
  }
  if (action.type === 'notFound') return action.sid === state.sid ? { ...initialChatState(state.epoch), revision: state.revision + 1, error: '该对话已不存在。' } : state
  if (action.type === 'failure') return state.phase === 'unknown' ? { ...state, recovered: false, recoveryError: action.message } : { ...state, phase: 'idle', pendingRequestId: null, connected: false, stage: null, error: action.message }
  if (action.type === 'sendFailed') return state.phase === 'sending' ? { ...state, phase: 'idle', pending: null, pendingRequestId: null, connected: false, stage: null, error: action.message, recovered: false, recoveryError: null, messages: action.unsent ? state.messages.slice(0, -1) : state.messages } : state
  if (action.type === 'checked') return state.phase === 'unknown' ? { ...state, recovered: true, recoveryError: null } : state
  if (action.type === 'serverSession') {
    if (!action.sid || action.sid === state.sid) return state
    if (state.phase !== 'sending' || !state.pending) return state
    if (state.sid === null) return { ...state, sid: action.sid, stage: null }
    const pending = state.messages.at(-1)
    if (!pending || pending.role !== 'user' || pending.content !== state.pending) return state
    return { ...state, sid: action.sid, stage: null, messages: [pending], proposals: {}, controlHistoryNeeded: false }
  }
  if (action.type === 'response') return state.phase === 'sending' ? finishMessage(state, { role: 'assistant', content: action.content, timestamp: new Date().toISOString(), ...messagePresentation(action), ...(action.device_result ? { device_result: action.device_result } : {}) }) : state
  if (action.type === 'unknown') return state.phase === 'sending' ? { ...state, phase: 'unknown', pendingRequestId: null, connected: false, stage: null, recovered: false, recoveryError: null, error: `${action.message ? `${action.message} ` : ''}发送结果尚不确定；上一条可能已送达，重发可能产生重复。` } : state
  return state
}
