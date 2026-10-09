export interface DeviceResult {
  entity_id: string
  name: string
  state: string
  observed_at: string
}
export type ControlStatus = 'pending' | 'executing' | 'succeeded' | 'failed' | 'unknown' | 'cancelled' | 'expired'
export interface ControlProposal {
  id: string
  session_id: string
  request_id: string
  entity_id: string
  name: string
  area_name: string
  action: 'turn_on' | 'turn_off'
  status: ControlStatus
  created_at: string
  expires_at: string
  before?: DeviceResult
  after?: DeviceResult
  error_code?: string
}
export interface ControlAttachment { kind: 'control_proposal'; proposal_id: string }
export interface ChatFrame {
  type?: string
  session_id?: string
  request_id?: string
  content?: string
  code?: string
  proposal?: ControlProposal
  device_result?: DeviceResult
}
export function deviceStateText(state: string): string {
  return ({ on: '开启', off: '关闭', unknown: '状态未知', unavailable: '暂不可用' } as Record<string, string>)[state] ?? state
}
export function observedTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '暂无观察时间' : date.toLocaleString('zh-CN')
}

// A cryptographic UUID also works on local HTTP, where randomUUID may be absent.
export function newChatRequestId(): string {
  if (globalThis.crypto.randomUUID) return globalThis.crypto.randomUUID()
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
export function isDeviceResult(value: unknown): value is DeviceResult {
  if (!value || typeof value !== 'object') return false
  const item = value as Record<string, unknown>
  return ['entity_id', 'name', 'state', 'observed_at'].every((key) => typeof item[key] === 'string')
}
export function isControlProposal(value: unknown): value is ControlProposal {
  if (!value || typeof value !== 'object') return false
  const item = value as Record<string, unknown>
  return ['id', 'session_id', 'request_id', 'entity_id', 'name', 'area_name', 'created_at', 'expires_at'].every((key) => typeof item[key] === 'string')
    && ['turn_on', 'turn_off'].includes(item.action as string)
    && ['pending', 'executing', 'succeeded', 'failed', 'unknown', 'cancelled', 'expired'].includes(item.status as string)
    && (item.before === undefined || isDeviceResult(item.before)) && (item.after === undefined || isDeviceResult(item.after))
}
