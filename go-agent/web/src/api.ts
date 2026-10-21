import { coordinatedFetch } from './coordinator'
import type { ControlAttachment, ControlProposal, DeviceResult, MessagePresentation } from './chat/control-types'

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
export interface ConsoleMessage extends MessagePresentation {
  role: string
  content: string
  timestamp: string
  attachments?: ControlAttachment[]
  device_result?: DeviceResult
}
export interface StoredAssistantMessage { index: number; message: ConsoleMessage }
export interface ControlProposalResponse extends ControlProposal {
  assistant_message?: StoredAssistantMessage
  assistant_message_unavailable?: boolean
}

export type ServiceMode = 'local' | 'cloud' | 'hybrid'
export type MiMoEndpoint = 'auto' | 'payg' | 'token_cn' | 'token_sgp' | 'token_ams'
export type TTSModel = 'mimo-v2.5-tts' | 'mimo-v2.5-tts-voiceclone'
export type TTSVoice = 'mimo_default' | '冰糖' | '茉莉' | '苏打' | '白桦' | 'Mia' | 'Chloe' | 'Milo' | 'Dean'
export interface AssistantSettings {
  mimo_endpoint?: MiMoEndpoint
  tts_model?: TTSModel; tts_voice?: TTSVoice
  revision: string; updated_at: string; key_configured: boolean
  llm_mode: ServiceMode; asr_mode: ServiceMode; tts_mode: ServiceMode
  local_asr_url: string; local_tts_url: string
}
export interface PersonalityMetadata { exists: boolean; size_bytes: number; updated_at: string; revision: string }
export interface VoiceReferenceMetadata { exists: boolean; size_bytes: number; updated_at: string; revision: string }
export interface SpeechStatus { asr_mode: ServiceMode; tts_mode: ServiceMode; tts_model?: TTSModel; tts_voice?: TTSVoice; voice_reference_configured?: boolean; cloud_configured: boolean; local_asr_configured: boolean; local_tts_configured: boolean }
export interface SpeechJob { id: string; status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'; text?: string; audio_base64?: string; mime_type?: string; error_code?: string }

const messages: Record<string, string> = {
  unauthenticated: '登录已失效，请重新登录。',
  forbidden: '没有权限执行此操作。',
  password_change_required: '请先修改临时密码。',
  invalid_request: '请检查输入内容后重试。',
  local_asr_url_invalid: '本地 ASR 地址须为有效的 HTTP 回环 IP 地址，且不含凭据、查询参数或片段。',
  local_tts_url_invalid: '本地 TTS 地址须为有效的 HTTP 回环 IP 地址，且不含凭据、查询参数或片段。',
  invalid_response: '服务响应格式异常，请联系管理员检查服务。',
  conflict: '信息已变化，请刷新后重试。',
  install_preexisting_unowned: 'HA 中已有同一标识的外部自动化，未执行写入。请修订或忽略这条待审核建议。',
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
  coordination_version_mismatch: '认证协调版本不兼容，请关闭旧标签后重新打开。',
  request_timeout: '等待请求超时；已提交的操作仍可能完成，请先核对结果，不要重复提交。',
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
  mode_changed: '服务模式已变化，请新建对话后继续。',
  invalid_chat_entry: '不支持此聊天入口。',
  request_id_required: '普通聊天需要请求标识，请重新发起请求。',
  request_entry_conflict: '此请求的内容或入口与原请求不一致，请核对会话。',
  request_entry_limit: '当前会话已达到请求上限，请新建会话。',
  speech_unavailable: '语音服务尚未就绪，请联系管理员检查配置。',
  speech_local_required: '此回复需本地朗读，但本地语音服务尚未配置。请联系管理员在助手设置中配置本地朗读服务。',
  speech_local_unconfigured: '当前选择本地朗读，但本地语音服务尚未配置。请联系管理员在助手设置中配置本地朗读服务，或为普通回复选择云端朗读。',
  speech_busy: '语音服务正在忙，请稍后重试。',
  speech_timeout: '语音处理超时，录音未自动重发。',
  speech_invalid: '语音内容或格式无效，请重新录制。',
  speech_cancelled: '语音任务已取消。',
  speech_failed: '语音处理失败，请检查服务配置后重试。',
  provider_unavailable: '所选服务暂不可用，请检查服务配置。',
  invalid_audio: '录音格式无效，请重新录制。',
  key_unavailable: '云端密钥未配置或无法读取，请联系管理员。',
  mimo_endpoint_mismatch: '套餐密钥与服务入口不匹配，请在助手设置选择对应入口。',
  mimo_endpoint_invalid: 'MiMo 服务入口配置无效，请在助手设置重新选择。',
  inference_auth_failed: '模型服务密钥无效或认证失败，请在助手设置检查密钥及服务入口。',
  inference_forbidden: '模型服务拒绝访问，请检查地区限制、密钥权限及套餐对应的服务入口。',
  inference_quota_exceeded: '模型服务额度不足或余额已用尽，请检查套餐用量。',
  inference_rate_limited: '模型服务请求过于频繁或套餐额度已用尽，请检查用量或稍后再试。',
  inference_content_filtered: '本次内容未通过模型服务的安全审核，请调整内容后再试。',
  inference_timeout: '模型服务响应超时，请检查服务连接。',
  inference_unavailable: '模型服务暂时不可用，请检查服务地址或稍后再试。',
  inference_invalid_response: '模型服务返回了无效响应，请检查模型及服务配置。',
  inference_invalid_request: '模型服务不接受当前请求，请检查模型、服务入口及参数配置。',
  inference_model_unavailable: '所选模型不存在或当前账号无权使用，请检查模型配置。',
  personality_unavailable: '无法读取人格文件，请管理员检查服务器 personality/default.md 的文件权限、UTF-8 编码和内容；修复或移除损坏文件后再上传。',
  assistant_settings_unavailable: '无法读取助手设置，请联系管理员检查配置存储。',
  intent_invalid: '无法解析本次设备需求，请补充设备名称和操作。',
  intent_unavailable: '设备需求识别服务暂时不可用，请检查模型配置。',
  context_budget_exceeded: '本轮必需上下文超过可用预算，请缩短当前问题或调整人格资料后重试。',
  chat_generation_failed: '本次回答生成失败，请检查模型服务配置。',
  chat_empty_response: '模型未返回有效回答，请检查模型服务配置。',
  chat_timeout: '本次对话处理超时，请检查服务连接。',
  chat_publish_failed: '对话结果未能确认保存，请核对历史记录。',
  session_not_found: '该对话已不存在，请新建对话。',
  control_unavailable: '设备服务暂时不可用，请检查家庭设备配置。',
  control_failed: '设备请求未完成，请核对设备状态。',
  control_conflict: '设备操作状态已变化，请重新核对。',
  control_expired: '设备操作提议已过期，请重新提出需求。',
  control_forbidden: '没有权限操作该设备。',
  control_invalid: '设备请求无效，请检查设备名称和操作。',
  control_not_found: '未找到该设备操作提议，请重新读取对话。',
  control_unsupported: '当前设备或操作尚不受支持。',
  control_capacity: '设备服务正在忙，请稍后再试。',
  expired: '操作提议已过期，请重新提出需求。',
  unsupported: '当前设备或操作尚不受支持。',
  unsupported_rule: '自动化规则包含不支持的条件或设备，请检查规则配置。',
  speech_auth_failed: '语音服务密钥无效或认证失败，请检查语音配置。',
  speech_forbidden: '语音服务访问受限，请检查地区限制、密钥权限和服务入口。',
  speech_quota_exceeded: '语音服务额度不足，请检查套餐用量。',
  speech_rate_limited: '语音请求过于频繁或套餐额度已用尽，请检查用量或稍后再试。',
  speech_content_filtered: '本次语音内容未通过服务的安全审核，请调整内容后再试。',
  speech_invalid_response: '语音服务返回异常，请检查语音服务配置。',
  speech_invalid_request: '语音服务不接受当前请求，请检查语音格式及配置。',
  speech_model_unavailable: '语音模型不可用，请检查模型和服务入口。',
  voice_reference_required: '克隆朗读需要参考音频，请管理员在助手设置中上传后重试。',
  voice_reference_decoder_unavailable: 'MP3 转换服务未配置，请先使用 PCM16 WAV 文件。',
  ollama_unavailable: '本地模型服务无法连接，请检查 Ollama 服务。',
  model_missing: '本地模型尚未安装，请联系管理员检查模型配置。',
  revision_conflict: '配置已由其他页面修改，请重新读取后保存。',
}

function apiErrorMessage(status: number, code: string): string {
  if (Object.hasOwn(messages, code)) return messages[code]
  const fallback: Record<number, string> = {
    400: '请求格式无效，请检查输入内容。',
    401: messages.unauthenticated, 403: messages.forbidden, 404: messages.not_found,
    408: '请求超时，请检查连接。', 409: messages.conflict, 410: messages.expired,
    413: '提交的内容过大，请缩减后再试。', 415: '提交的内容格式不受支持。',
    422: messages.invalid_request, 429: messages.rate_limited,
    500: '服务内部处理失败，请联系管理员。',
    502: '上游服务响应异常，请检查服务配置。',
    503: messages.unavailable, 504: '上游服务响应超时，请检查服务连接。',
  }
  const message = fallback[status] ?? '服务未能完成请求，请联系管理员。'
  const safeCode = /^[a-z][a-z0-9_]{0,63}$/.test(code) ? code : 'unknown'
  return `${message}（错误代码：${safeCode}${status > 0 ? `；HTTP ${status}` : ''}）`
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, public requestId?: string) {
    super(apiErrorMessage(status, code))
    this.name = 'ApiError'
  }
}

export function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.requestId && /^[A-Za-z0-9_-]{1,128}$/.test(error.requestId) ? `${error.message} 请求编号：${error.requestId}` : error.message
  if (error instanceof DOMException && error.name === 'AbortError') return ''
  return '网络连接失败，请检查本地服务后重试。'
}

type RequestOptions = { method?: 'GET' | 'POST' | 'PATCH' | 'PUT'; body?: object; csrfToken?: string; signal?: AbortSignal; expected?: { userId: string; csrfToken: string } }

async function consoleResponse(path: string, options: RequestOptions = {}): Promise<Response> {
  const headers: Record<string, string> = {}
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (options.csrfToken) headers['X-CSRF-Token'] = options.csrfToken
  return coordinatedFetch(`${API_ROOT}${path}`, {
    method: options.method ?? 'GET',
    credentials: 'same-origin',
    cache: 'no-store',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  }, path === '/auth/login' || path === '/auth/logout' || path === '/auth/password', options.expected)
}

export async function requestConsole<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const response = await consoleResponse(path, options)
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
  try { return await response.json() as T }
  catch (cause) {
    if (cause instanceof SyntaxError) throw new ApiError(502, 'invalid_response')
    throw cause
  }
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
export interface SuggestionIntent { schema_version: number; kind: 'time' | 'correlation'; entity_id: string; related_id?: string; at?: string; time_zone: string; required_conditions: string[] }
export interface SuggestionRevisionChange { field: 'time' | 'source' | 'target' | 'presence_entity' | 'presence_state'; before: string; after: string }
export interface ManagedVersion { id: string; intent: SuggestionIntent; presence_binding?: { entity_id: string; state: string }; diff: SuggestionRevisionChange[] }
export interface ManagedRevisionRequest { expected_version: string; intent: SuggestionIntent; presence_binding?: { entity_id: string; state: string } }
export interface ManagedDraft { id: string; managed_id: string; current_operation_id: string; current_version: string; expected_digest: string; digest: string; created_at: string; version: ManagedVersion }
export type ManagementAction = 'enable' | 'disable' | 'update' | 'delete'
export interface ManagementOperation {
 id: string; managed_id: string; action: ManagementAction; status: 'prepared' | 'applying' | 'confirmed' | 'failed' | 'unknown' | 'expired' | 'cancelled'
 current_operation_id: string; current_version: string; expected_digest: string; revision_id?: string; revision?: ManagedVersion; stop_actions?: boolean
 created_at: string; updated_at: string; expires_at: string; attempted_at?: string; write_status: string; error_code?: string
 backup_status: string; archive_status: string; installation?: InstallOperationView; observed_state?: string; observed_at?: string
 deletion?: { config_absent: boolean; mapping_absent: boolean; state_absent: boolean; checked_at: string }
 can_confirm: boolean; can_cancel: boolean; can_reconcile: boolean; concurrency_risk: string
}
export interface ManagementView {
 managed_id: string; suggestion_id: string; current_operation_id: string; current_version: string; expected_digest: string; current: ManagedVersion
 shared: boolean; deleted_at?: string; can_prepare: boolean; latest_operation: ManagementOperation | null; draft: ManagedDraft | null
}
export interface ManagementPartial {
 id: string; managed_id: string; action: ManagementAction; status: 'unknown'; observed_status: string; write_status: string
 created_at: string; expires_at: string; attempted_at?: string; backup_status: string; archive_status: string
 local_persistence: 'unverified'; result_source: 'response_observation'; can_confirm: false; can_cancel: false; can_reconcile: false
}
export class ManagementPartialError extends ApiError {
 constructor(public operation: ManagementPartial, requestId?: string) { super(503, 'management_persistence_unverified', requestId) }
}
function managementPartial(value: unknown): ManagementPartial | undefined {
 if (!value || typeof value !== 'object') return
 const p = value as Record<string, unknown>
 if (p.status !== 'unknown' || p.local_persistence !== 'unverified' || p.result_source !== 'response_observation' || p.can_confirm !== false || p.can_cancel !== false || p.can_reconcile !== false) return
 if (![p.id,p.managed_id].every(v => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v))) return
 if (!['enable','disable','update','delete'].includes(String(p.action)) || !['prepared','applying','confirmed','failed','unknown','expired','cancelled'].includes(String(p.observed_status)) || !['not_attempted','acknowledged','unknown','rejected'].includes(String(p.write_status))) return
 if (!['not_required','unknown','saved'].includes(String(p.backup_status)) || !['not_required','unknown'].includes(String(p.archive_status))) return
 if (![p.created_at,p.expires_at,...(p.attempted_at === undefined ? [] : [p.attempted_at])].every(v => typeof v === 'string' && Number.isFinite(Date.parse(v)))) return
 return { id:p.id as string,managed_id:p.managed_id as string,action:p.action as ManagementAction,status:'unknown',observed_status:p.observed_status as string,write_status:p.write_status as string,created_at:p.created_at as string,expires_at:p.expires_at as string,...(p.attempted_at ? {attempted_at:p.attempted_at as string}:{}),backup_status:p.backup_status as string,archive_status:p.archive_status as string,local_persistence:'unverified',result_source:'response_observation',can_confirm:false,can_cancel:false,can_reconcile:false }
}
// Only management operations have an approved non-2xx result envelope. Keep
// transport/session coordination identical to ordinary requests, with no replay.
export async function requestManagementAction(id: string, action: 'confirm' | 'cancel' | 'reconcile', csrfToken: string, signal?: AbortSignal): Promise<{ operation: ManagementOperation }> {
 return managementWrite('/admin/automation-actions/' + encodeURIComponent(id) + '/' + action, {}, csrfToken, signal, id)
}
export async function requestManagementPrepare(id: string, body: {action:ManagementAction;expected_digest:string;revision_id?:string;stop_actions?:boolean},csrfToken:string,signal?:AbortSignal): Promise<{operation:ManagementOperation}> {
 return managementWrite('/admin/automations/'+encodeURIComponent(id)+'/actions',body,csrfToken,signal,undefined,id)
}
async function managementWrite(path:string,body:object,csrfToken:string,signal?:AbortSignal,operationId?:string,managedId?:string):Promise<{operation:ManagementOperation}> {
 const response = await consoleResponse(path, { method:'POST',body,csrfToken,signal })
 let data: any
 try { data = await response.json() } catch { throw new ApiError(response.ok ? 502 : response.status, 'invalid_response') }
 if (!response.ok) {
  const code = typeof data?.error?.code === 'string' ? data.error.code : 'unknown'
  const requestId = typeof data?.error?.request_id === 'string' ? data.error.request_id : undefined
  const partial = response.status === 503 && code === 'management_persistence_unverified' ? managementPartial(data.operation) : undefined
  if (partial && (!operationId || partial.id === operationId) && (!managedId || partial.managed_id === managedId)) throw new ManagementPartialError(partial, requestId)
  throw new ApiError(response.status,code,requestId)
 }
 if (!data?.operation || operationId && data.operation.id !== operationId || managedId && data.operation.managed_id !== managedId) throw new ApiError(502,'invalid_response')
 return data as { operation: ManagementOperation }
}
export interface InstallOperationView {
 id: string; suggestion_id: string; suggestion_version: string
 status: 'applying' | 'unknown' | 'failed' | 'confirmed'; phase: string; error_code?: string
 created_at: string; updated_at: string
 config_status: 'unknown' | 'saved' | 'matching' | 'drift' | 'missing'
 mapping_status: 'unknown' | 'verified'; load_status: 'unknown' | 'verified' | 'drift'
 archive_status: 'not_ready' | 'pending' | 'complete'; archive_error_code?: string
 ownership: 'managed' | 'unverified' | 'historical'; observed_state?: 'on' | 'off' | 'unknown' | 'unavailable'; observed_at?: string
 can_reconcile: boolean; legacy_unverified: boolean; historical?: boolean; deleted_at?: string
}
export interface SuggestionView {
 installation?: InstallOperationView; installation_state?: 'verified' | 'unknown' | 'legacy_unverified' | 'deleted'; current_version?: string
  can_revise?: boolean; intent?: SuggestionIntent; presence_binding?: { entity_id: string; state: string }; revision_diff?: SuggestionRevisionChange[]
  entities?: (Pick<EntityView, 'entity_id' | 'name' | 'domain' | 'device_class'> & { area_name: string; device_name: string })[]
  id: string; status: 'pending' | 'superseded' | 'applying' | 'failed' | 'confirmed' | 'ignored' | 'unknown'
  created_at: string; title: string; confidence: number; time_zone: string; entity_ids: string[]
  rule: SuggestionRule | null; missing_bindings: string[]; unsupported_code: string | null
  shared: boolean; can_bind: boolean; can_confirm: boolean; can_ignore: boolean
  source_suggestion_id?: string; superseded_by?: string
  evidence: SuggestionEvidenceView
}
export interface SharedAutomationResult {
 id:string; created_at:string; title:string; time_zone:string; rule:SuggestionRule
 entity_ids:string[]; entities:NonNullable<SuggestionView['entities']>; evidence:SuggestionEvidenceView
 shared:boolean; historical:true; history_status:'recorded'|'legacy_unverified'|'updated'|'deleted'; deleted_at?:string
}
export interface SuggestionList { suggestions: SuggestionView[]; count: number; shared_results?:SharedAutomationResult[] }
export interface AutomationRef { kind: 'managed' | 'external' | 'unverified'; id: string }
export interface AutomationDiagnosticCheck {
 status: 'pass' | 'finding' | 'unknown'; category: string; severity: 'info' | 'warning' | 'error'; reason: string; next_action: string
 evidence_refs: { source: string; rule_ref: AutomationRef; entity_ids: string[]; observed_at: string }[]
}
export interface AutomationDiagnostic {
 historical?: boolean
 id: string; ref: AutomationRef; checked_at: string; window: TimeWindow; summary: 'attention' | 'partial' | 'pass' | 'unknown'
 checks: AutomationDiagnosticCheck[]; changes: { kind: 'new' | 'severity_changed' | 'recovered'; check: AutomationDiagnosticCheck }[]
 counts: { pass: number; finding: number; unknown: number; changes: number }; sources: Record<string, { status: string; observed_at: string }>
}
export interface AutomationView {
 id: string; suggestion_id: string; current_version?: string; observation_version?: string; deleted_at?: string; rule?: SuggestionRule; entities?: SuggestionView['entities']; ref?: AutomationRef; title: string; ownership: 'managed' | 'unverified' | 'historical'
 installation_state: 'verified' | 'unknown' | 'deleted'; observed_state: 'on' | 'off' | 'unknown'; observed_at?: string; stale: boolean
 diagnostic?: AutomationDiagnostic; can_diagnose: boolean; ha_url?: string
}
export interface AutomationList {
 automations: AutomationView[]; external_candidates: { ref: AutomationRef; title: string; ha_url?: string }[]
 counts: { installed: number; attention: number; unverified: number; archived?: number }; sharing_revision?: number
}
export interface SharingView { revision: number; suggestion_ids: string[] }
export interface CollectionView {
  target_days?: number; requested_window?: TimeWindow | null; coverage?: HistoryCoverage | null; coverage_error?: string; retention?: string
  analysis_runs?: AnalysisRun[]; backfill_runs?: BackfillRun[]; latest_analysis?: AnalysisRun | null
  next_analysis_at?: string | null; next_analysis_reason?: string
  phase: 'idle' | 'snapshot' | 'history' | 'failed'; last_attempt_at: string | null
  last_success_at: string | null; last_failure_at: string | null; error_code: string | null
  snapshot_at: string | null; window_start: string | null; window_end: string | null
  completed_batches: number; total_batches: number; checkpoint: string | null
}


export interface TimeWindow { start: string; end: string }
export type RunStatus = 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'
export interface RunAccepted { run_id: string; status: RunStatus; cancel_requested?: boolean }
export interface HistoryCoverage {
 requested_union_duration: number; successful_union_duration: number; corrupt_files: number
 entities: Record<string, { requested_union_duration: number; successful_union_duration: number; valid_state_count: number; change_count: number; gaps: TimeWindow[]; retention: string }>
}
export interface PatternOutcome {
 applicable: boolean; outcome: 'matched' | 'no_pattern' | 'insufficient_data'; reason: string
 candidates: { entity_id: string; related_id?: string; observation_days: number; required_days: number; source_transitions: number; source_transition_days: number; numeric_samples: number; outcome: string; reason: string }[] | null
}
export interface AnalysisRun {
 id: string; status: RunStatus; source?: 'manual' | 'scheduled'; requested_window: TimeWindow; home_timezone: string
 created_at: string; started_at?: string; finished_at?: string; algorithm_version: string; error_code?: string
 outcome?: 'matched' | 'no_pattern' | 'insufficient_data'; outcomes?: Record<string, PatternOutcome>; coverage: HistoryCoverage
 counts: { entries: number; patterns: number; suggestions: number; excluded: Record<string, number> }
}
export interface BackfillRun {
 id: string; status: RunStatus; requested_window: TimeWindow; cursor: string; completed_batches: number; gaps: TimeWindow[]
 retention: string; created_at: string; updated_at: string; error_code?: string; last_failure_code?: string
}
export interface SuggestionEvidenceView {
 sample_size: number | null; period_days: number | null; hit_days?: number | null; eligible_days?: number | null
 denominator?: number | null; denominator_type?: string; window?: TimeWindow; algorithm_version?: string
 excluded_counts?: Record<string, number>; event_refs?: { entity_id: string; at: string }[]; evidence_count?: number
}
