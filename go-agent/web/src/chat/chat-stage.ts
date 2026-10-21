import type { ChatStage, ChatStageFrame } from './control-types'

export const chatStageText: Record<ChatStage, string> = {
  queued: '正在排队',
  preparing: '正在准备',
  classifying: '正在理解请求',
  generating: '正在生成回答',
  saving: '正在保存',
}

export function parseChatStageFrame(value: unknown): ChatStageFrame | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const frame = value as Record<string, unknown>
  if (frame.type !== 'chat_stage'
    || typeof frame.session_id !== 'string' || !frame.session_id.trim()
    || typeof frame.request_id !== 'string' || !frame.request_id.trim()
    || typeof frame.stage !== 'string' || !Object.hasOwn(chatStageText, frame.stage)) return null
  return { type: 'chat_stage', session_id: frame.session_id, request_id: frame.request_id, stage: frame.stage as ChatStage }
}
