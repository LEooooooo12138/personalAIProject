import { coordinatedFetch } from './coordinator'
import type { ControlAttachment, DeviceResult } from './chat/control-types'

const API_ROOT = '/api/console/v1'

export type Role = 'admin' | 'member'
export interface ConsoleUser {
  id: string
  username: string
  display_name: string
  role: Role
  must_change_password: boolean
}
export interface Member extends ConsoleUser { disabled: boolean }
export interface Identity {
  user: ConsoleUser
  capabilities: string[]
  csrf_token: string
}
export interface ConsoleSession {
  id: string
  started_at: string
  last_active_at: string
  round_count: number
  message_count: number
  preview: string
}
export interface ConsoleMessage {
  role: string
  content: string
  timestamp: string
  attachments?: ControlAttachment[]
  device_result?: DeviceResult
}

const messages: Record<string, string> = {
  unauthenticated: '登录已失效，请重新登录。',
  forbidden: '没有权限执行此操作。',
  password_change_required: '请先修改临时密码。',
  invalid_request: '请检查输入内容后重试。',
  conflict: '信息已变化，请刷新后重试。',
  busy: '分析正在进行，请稍后刷新。',
  rate_limited: '尝试过于频繁，请稍后再试。',
  csrf_failed: '页面验证已失效，请重新登录。',
  origin_denied: '请求来源未获授权。',
  not_initialized: '家庭控制台尚未初始化，请联系管理员。',
  not_enabled: '家庭控制台尚未启用。',
  unavailable: '服务暂时不可用，请稍后重试。',
  session_unavailable: '暂时无法读取对话记录。',
  not_found: '未找到请求的内容。',
  identity_changed: '登录身份已变化，请重新登录。',
  coordination_unsupported: '当前浏览器不支持安全认证协调，请使用支持 SharedWorker 的浏览器。',
  coordination_unavailable: '浏览器无法安全协调多个页面，请关闭其他页面后重试。',
  ha_unavailable: 'HA 暂时无法连接，请稍后重新加载。',
  ha_auth_required: '家庭设备服务授权失效，请联系管理员。',
  ha_forbidden: '家庭设备服务权限不足，请联系管理员。',
  ha_timeout: '家庭设备服务请求超时，请稍后重新检查。',
  ha_invalid_response: '家庭设备服务返回异常，请联系管理员。',
  ha_not_configured: '家庭设备服务尚未配置。',
  catalog_unavailable: '家庭目录暂时不可用，请稍后重新加载。',
  network: '网络连接失败，请检查本地服务后重试。',
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, public requestId?: string) {
    super(messages[code] ?? '请求未完成，请稍后重试。')
    this.name = 'ApiError'
  }
}

export function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.requestId ? `${error.message} 请求编号：${error.requestId}` : error.message
  if (error instanceof DOMException && error.name === 'AbortError') return ''
  return '网络连接失败，请检查本地服务后重试。'
}

type RequestOptions = { method?: 'GET' | 'POST' | 'PATCH' | 'PUT'; body?: object; csrfToken?: string; signal?: AbortSignal; expected?: { userId: string; csrfToken: string } }

export async function requestConsole<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (options.csrfToken) headers['X-CSRF-Token'] = options.csrfToken
  const response = await coordinatedFetch(`${API_ROOT}${path}`, {
    method: options.method ?? 'GET',
    credentials: 'same-origin',
    cache: 'no-store',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  }, path === '/auth/login' || path === '/auth/logout' || path === '/auth/password', options.expected)
  if (!response.ok) {
    let code = 'unknown'
    let requestId: string | undefined
    try {
      const payload: unknown = await response.json()
      if (payload && typeof payload === 'object' && 'error' in payload) {
        const detail = payload.error
        if (detail && typeof detail === 'object') {
          if ('code' in detail && typeof detail.code === 'string') code = detail.code
          if ('request_id' in detail && typeof detail.request_id === 'string') requestId = detail.request_id
        }
      }
    } catch { /* Non-JSON proxy errors still receive a safe local message. */ }
    throw new ApiError(response.status, code, requestId)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const consoleApi = {
  status: (signal?: AbortSignal) => requestConsole<{ initialized: boolean }>('/auth/status', { signal }),
  me: (signal?: AbortSignal) => requestConsole<Identity>('/auth/me', { signal }),
  login: (username: string, password: string, signal?: AbortSignal) => requestConsole<Identity>('/auth/login', { method: 'POST', body: { username, password }, signal }),
  logout: (csrfToken: string, signal?: AbortSignal, expected?: { userId: string; csrfToken: string }) => requestConsole<void>('/auth/logout', { method: 'POST', csrfToken, signal, expected }),
  changePassword: (oldPassword: string, newPassword: string, csrfToken: string, signal?: AbortSignal, expected?: { userId: string; csrfToken: string }) => requestConsole<void>('/auth/password', { method: 'POST', body: { old_password: oldPassword, new_password: newPassword }, csrfToken, signal, expected }),
  sessions: (signal?: AbortSignal) => requestConsole<{ sessions: ConsoleSession[] }>('/sessions', { signal }),
  messages: (id: string, signal?: AbortSignal) => requestConsole<{ id: string; messages: ConsoleMessage[] }>(`/sessions/${encodeURIComponent(id)}/messages`, { signal }),
  members: (signal?: AbortSignal) => requestConsole<{ members: Member[] }>('/admin/members', { signal }),
  createMember: (value: { username: string; display_name: string }, csrfToken: string, signal?: AbortSignal) => requestConsole<{ user: Member; temporary_password: string }>('/admin/members', { method: 'POST', body: value, csrfToken, signal }),
  updateMember: (id: string, value: { display_name?: string; disabled?: boolean }, csrfToken: string, signal?: AbortSignal) => requestConsole<{ user: Member }>(`/admin/members/${encodeURIComponent(id)}`, { method: 'PATCH', body: value, csrfToken, signal }),
  resetMemberPassword: (id: string, csrfToken: string, signal?: AbortSignal) => requestConsole<{ temporary_password: string }>(`/admin/members/${encodeURIComponent(id)}/reset-password`, { method: 'POST', csrfToken, signal }),
}


// Read-only family projection. No HA credentials or registry attributes cross this boundary.
export interface CatalogMeta {
  observed_at: string | null
  last_attempt_at: string | null
  last_success_at: string | null
  freshness: 'fresh' | 'stale' | 'unknown'
  connection: 'connected' | 'unavailable' | 'unknown'
  error_code: string | null
}
export interface EntityView {
  entity_id: string
  name: string
  domain: string
  state: string | null
  device_class?: string | null
  unit: string | null
  last_changed: string | null
  last_updated: string | null
  disabled: boolean
  hidden: boolean
  category: string | null
}
export interface DeviceView {
  id: string
  kind: 'device' | 'entity'
  name: string
  area_id: string
  direct_area_id: string | null
  membership: 'direct' | 'entity' | 'both'
  entity_count: number
  domains: string[]
  entities: EntityView[]
  load_location_verified: false
}
export interface AreaSummary {
  id: string
  name: string
  device_count: number
  entity_count: number
  standalone_entity_count: number
  matches: { id: string; name: string; kind: 'device' | 'entity' }[]
}
export interface AreaList {
  meta: CatalogMeta
  totals: { devices: number; entities: number; standalone_entities: number }
  areas: AreaSummary[]
}
export interface AreaDevices { meta: CatalogMeta; area: { id: string; name: string }; devices: DeviceView[] }
export interface AreaDeviceDetail { meta: CatalogMeta; area: { id: string; name: string }; device: DeviceView }
export interface IntegrationsStatus {
  ha: CatalogMeta
  ollama: { connection: 'connected' | 'unavailable' | 'unknown'; checked_at: string | null; models: { name: string; available: boolean | null }[]; error_code: string | null }
}

export interface KnowledgeSearch { results: { path: string; title: string; snippet: string }[]; count: number }
export interface KnowledgePageView { path: string; title: string; body: string }
export interface VaultStatus { vault: 'personal'; page_count: number; total_bytes: number }
export interface KnowledgeAnswer { answer: string; sources: { path: string; title: string; score: number }[] }
export interface KnowledgeIngest { vault: 'personal'; path: string; title: string }
export interface SuggestionRule {
  triggers: { platform: string; at?: string; entity_id?: string; from?: string; to?: string }[]
  conditions: { condition: string; after?: string; entity_id?: string; state?: string }[]
  actions: { service: string; target: { entity_id: string } }[]
}
export interface SuggestionView {
  id: string; status: 'pending' | 'superseded' | 'applying' | 'failed' | 'confirmed' | 'ignored'
  created_at: string; title: string; confidence: number; time_zone: string; entity_ids: string[]
  rule: SuggestionRule | null; missing_bindings: string[]; unsupported_code: string | null
  shared: boolean; can_bind: boolean; can_confirm: boolean; can_ignore: boolean
  source_suggestion_id?: string; superseded_by?: string
  evidence: { sample_size: number | null; period_days: number | null }
}
export interface SuggestionList { suggestions: SuggestionView[]; count: number }
export interface SharingView { revision: number; suggestion_ids: string[] }
export interface CollectionView {
  phase: 'idle' | 'snapshot' | 'history' | 'failed'; last_attempt_at: string | null
  last_success_at: string | null; last_failure_at: string | null; error_code: string | null
  snapshot_at: string | null; window_start: string | null; window_end: string | null
  completed_batches: number; total_batches: number; checkpoint: string | null
}
export interface AnalysisView { period_start: string; period_end: string; suggestion_count: number }
