import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChatPage from './ChatPage'
import { ApiError, type ConsoleMessage, type ConsoleSession } from '../api'

const { sessions, messages, openChatSocket, capabilities, authSupport } = vi.hoisted(() => ({
  sessions: vi.fn(async (): Promise<{ sessions: ConsoleSession[] }> => ({ sessions: [] })),
  messages: vi.fn(async (_sid: string): Promise<{ id: string; messages: ConsoleMessage[] }> => ({ id: 'one', messages: [] })),
  openChatSocket: vi.fn(),
  capabilities: { value: ['chat:use', 'sessions:read'] as string[] },
  authSupport: { isCurrent: (epoch: number): boolean => epoch === 1, expire: vi.fn(), signal: new AbortController().signal, epoch: 1, userId: 'alice' },
}))
vi.mock('../api', async (importOriginal) => ({ ...(await importOriginal<typeof import('../api')>()), consoleApi: { sessions, messages, me: async () => ({ user: { id: 'alice' } }) } }))
vi.mock('../coordinator', () => ({ openChatSocket, coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options) }))
vi.mock('../auth', () => ({ useAuth: () => ({
  identity: { user: { id: authSupport.userId, username: 'alice', display_name: 'Alice', role: 'member', must_change_password: false }, capabilities: capabilities.value, csrf_token: 'memory-only' },
  epoch: authSupport.epoch, signal: authSupport.signal, isCurrent: authSupport.isCurrent, expire: authSupport.expire,
}) }))

class FakeSocket {
  static sockets: FakeSocket[] = []
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  sent: string[] = []
  closed = false
  constructor(_url: string) { FakeSocket.sockets.push(this) }
  send(value: string) { this.sent.push(value) }
  close() { this.closed = true; this.onclose?.() }
  open() { this.onopen?.() }
  receive(value: object) { this.onmessage?.({ data: JSON.stringify(value) } as MessageEvent) }
}

beforeEach(() => {
  FakeSocket.sockets = []
  sessions.mockReset()
  messages.mockReset()
  sessions.mockResolvedValue({ sessions: [] })
  messages.mockResolvedValue({ id: 'one', messages: [] })
  openChatSocket.mockReset()
  openChatSocket.mockImplementation(() => new Promise<WebSocket>((resolve) => {
    const socket = new FakeSocket('')
    socket.onopen = () => resolve(socket as unknown as WebSocket)
  }))
  capabilities.value = ['chat:use', 'sessions:read']
  authSupport.isCurrent = (epoch: number) => epoch === 1
  authSupport.signal = new AbortController().signal
  authSupport.epoch = 1; authSupport.userId = 'alice'
  vi.stubGlobal('fetch', vi.fn())
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }))
  vi.stubGlobal('WebSocket', FakeSocket)
})

function LocationProbe() { const location = useLocation(); return <output data-testid="location">{location.search}</output> }

describe('chat composer', () => {
  it('ignores empty text and IME Enter, then sends once despite repeated clicks', async () => {
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    const input = await screen.findByRole('textbox', { name: '消息内容' })
    const send = screen.getByRole('button', { name: '发送消息' })
    fireEvent.click(send)
    expect(FakeSocket.sockets).toHaveLength(0)
    fireEvent.change(input, { target: { value: '你好' } })
    fireEvent.compositionStart(input)
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(FakeSocket.sockets).toHaveLength(0)
    fireEvent.compositionEnd(input)
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(openChatSocket).toHaveBeenCalledWith(expect.objectContaining({ user: expect.objectContaining({ id: 'alice' }) }), expect.any(AbortSignal))
    await waitFor(() => expect(FakeSocket.sockets).toHaveLength(1))
    fireEvent.click(send)
    FakeSocket.sockets[0].open()
    await waitFor(() => expect(FakeSocket.sockets[0].sent.map((value) => JSON.parse(value))).toEqual([expect.objectContaining({ session_id: '', content: '你好', request_id: expect.any(String) })]))
  })

  it('keeps the newly selected SID when an old history request returns 404 late', async () => {
    let rejectOld: (reason: unknown) => void = () => {}
    messages.mockImplementation((sid: string) => sid === 'old'
      ? new Promise((_resolve, reject) => { rejectOld = reject })
      : Promise.resolve({ id: 'new', messages: [{ role: 'assistant', content: '新对话内容', timestamp: '2026-01-01T00:00:00Z' }] }))
    sessions.mockResolvedValue({ sessions: [
      { id: 'old', started_at: '2026-01-01', last_active_at: '2026-01-01', round_count: 1, message_count: 1, preview: '旧对话' },
      { id: 'new', started_at: '2026-01-02', last_active_at: '2026-01-02', round_count: 1, message_count: 1, preview: '新对话' },
    ] })
    render(<MemoryRouter initialEntries={['/app/chat?sid=old']}><LocationProbe /><ChatPage /></MemoryRouter>)
    await waitFor(() => expect(messages).toHaveBeenCalledWith('old', expect.any(AbortSignal)))
    await screen.findByRole('button', { name: /新对话.*条消息/ })
    fireEvent.click(screen.getByRole('button', { name: /新对话.*条消息/ }))
    await screen.findByText('新对话内容')
    await act(async () => rejectOld(new ApiError(404, 'not_found')))
    expect(screen.getByTestId('location')).toHaveTextContent('sid=new')
    expect(screen.getByText('新对话内容')).toBeVisible()
  })

  it('checks history after a lost socket and only retransmits after the user chooses retry', async () => {
    let resolveHistory: (value: { id: string; messages: ConsoleMessage[] }) => void = () => {}
    messages.mockImplementation(() => new Promise((resolve) => { resolveHistory = resolve }))
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '可能已送达' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    const first = FakeSocket.sockets[0]
    first.open()
    await waitFor(() => expect(first.sent).toHaveLength(1))
    await act(async () => first.receive({ type: 'session', session_id: 'server-one' }))
    expect(first.closed).toBe(false)
    await act(async () => first.close())
    await waitFor(() => expect(messages).toHaveBeenCalledWith('server-one', expect.any(AbortSignal)))
    expect(screen.queryByRole('button', { name: '重试上一条' })).toBeNull()
    await act(async () => resolveHistory({ id: 'server-one', messages: [{ role: 'user', content: '可能已送达', timestamp: '2026-01-01T00:00:00Z' }] }))
    await screen.findByRole('button', { name: '重试上一条' })
    expect(FakeSocket.sockets).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: '重试上一条' }))
    expect(FakeSocket.sockets).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    expect(FakeSocket.sockets).toHaveLength(2)
    FakeSocket.sockets[1].open()
    await waitFor(() => expect(FakeSocket.sockets[1].sent.map((value) => JSON.parse(value))).toEqual([expect.objectContaining({ session_id: 'server-one', content: '可能已送达', request_id: JSON.parse(first.sent[0]).request_id })]))
  })

  it('keeps duplicate-risk warning and blocks resend or continue when history recovery fails', async () => {
    messages.mockResolvedValueOnce({ id: 'existing', messages: [] }).mockRejectedValueOnce(new ApiError(503, 'session_unavailable'))
    render(<MemoryRouter initialEntries={['/app/chat?sid=existing']}><ChatPage /></MemoryRouter>)
    await waitFor(() => expect(messages).toHaveBeenCalledTimes(1))
    await screen.findByRole('textbox', { name: '消息内容' })
    fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '可能重复' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    const socket = FakeSocket.sockets[0]
    socket.open()
    await waitFor(() => expect(socket.sent).toHaveLength(1))
    await act(async () => socket.close())
    await waitFor(() => expect(messages).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('alert')).toHaveTextContent('重发可能产生重复')
    expect(screen.getByRole('alert')).toHaveTextContent('暂时无法读取对话记录')
    expect(screen.queryByRole('button', { name: '重试上一条' })).toBeNull()
    expect(screen.queryByRole('button', { name: '继续对话，不重发' })).toBeNull()
    messages.mockResolvedValueOnce({ id: 'existing', messages: [] })
    fireEvent.click(screen.getByRole('button', { name: '再次核对历史' }))
    await screen.findByRole('button', { name: '重试上一条' })
    expect(screen.getByRole('button', { name: '继续对话，不重发' })).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: '继续对话，不重发' }))
    expect(FakeSocket.sockets).toHaveLength(1)
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
  })

  it('offers read-only history without new-chat or send prompts when chat capability is absent', async () => {
    capabilities.value = ['sessions:read']
    sessions.mockResolvedValue({ sessions: [{ id: 'one', preview: '旧记录', started_at: '2026-01-01', last_active_at: '2026-01-01', round_count: 1, message_count: 1 }] })
    messages.mockResolvedValue({ id: 'one', messages: [{ role: 'assistant', content: '只读内容', timestamp: '2026-01-01T00:00:00Z' }] })
    render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage /></MemoryRouter>)
    await screen.findByText('只读内容')
    expect(screen.getByRole('heading', { name: '对话记录' })).toBeVisible()
    expect(screen.queryByRole('button', { name: '新对话' })).toBeNull()
    expect(screen.queryByRole('button', { name: '发送消息' })).toBeNull()
    expect(screen.queryByText(/发一条消息|今天想聊/)).toBeNull()
  })

  it('keeps a new chat uncertain when session-list recovery fails, then offers only explicit resend', async () => {
    sessions.mockResolvedValueOnce({ sessions: [] }).mockRejectedValueOnce(new ApiError(503, 'unavailable')).mockResolvedValueOnce({ sessions: [] })
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    await waitFor(() => expect(sessions).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '新问题' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    FakeSocket.sockets[0].open()
    await waitFor(() => expect(FakeSocket.sockets[0].sent).toHaveLength(1))
    await act(async () => FakeSocket.sockets[0].close())
    await waitFor(() => expect(sessions).toHaveBeenCalledTimes(2))
    expect(document.querySelector('.chat-alert')).toHaveTextContent('重发可能产生重复')
    expect(document.querySelector('.chat-alert')).toHaveTextContent('服务暂时不可用')
    expect(screen.queryByRole('button', { name: '重试上一条' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '再次核对历史' }))
    await screen.findByRole('button', { name: '重试上一条' })
    expect(screen.queryByRole('button', { name: '继续对话，不重发' })).toBeNull()
    expect(FakeSocket.sockets).toHaveLength(1)
  })

  it('shows only one question bubble after an explicit retry before any server SID arrives', async () => {
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    const input = await screen.findByRole('textbox', { name: '消息内容' })
    fireEvent.change(input, { target: { value: '新对话问题' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    const first = FakeSocket.sockets[0]
    first.open()
    await waitFor(() => expect(first.sent).toHaveLength(1))
    await act(async () => first.close())
    await screen.findByRole('button', { name: '重试上一条' })
    expect(document.querySelectorAll('.chat-bubble.from-user')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: '重试上一条' }))
    expect(input).toHaveValue('新对话问题')
    expect(FakeSocket.sockets).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    expect(FakeSocket.sockets).toHaveLength(2)
    expect(document.querySelectorAll('.chat-bubble.from-user')).toHaveLength(1)
  })

  it('closes a socket granted after switching sessions without sending the stale question', async () => {
    let grant: (socket: WebSocket) => void = () => {}
    openChatSocket.mockImplementationOnce(() => new Promise<WebSocket>((resolve) => { grant = resolve }))
    sessions.mockResolvedValue({ sessions: [{ id: 'saved', preview: '已保存记录', started_at: '2026-01-01', last_active_at: '2026-01-01', round_count: 1, message_count: 1 }] })
    messages.mockResolvedValue({ id: 'saved', messages: [{ role: 'assistant', content: '历史内容', timestamp: '2026-01-01T00:00:00Z' }] })
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    const saved = await screen.findByRole('button', { name: /已保存记录.*条消息/ })
    fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '不要送到旧会话' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    expect(FakeSocket.sockets).toHaveLength(0)
    const handshakeSignal = openChatSocket.mock.calls[0][1] as AbortSignal
    expect(handshakeSignal.aborted).toBe(false)
    fireEvent.click(saved)
    expect(handshakeSignal.aborted).toBe(true)
    await screen.findByText('历史内容')
    const staleSocket = new FakeSocket('')
    await act(async () => grant(staleSocket as unknown as WebSocket))
    expect(staleSocket.closed).toBe(true)
    expect(staleSocket.sent).toEqual([])
    expect(screen.getByText('历史内容')).toBeVisible()
    expect(screen.queryByText('不要送到旧会话')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('does not mark a previous identity unknown when its queued socket request aborts', async () => {
    let rejectGrant: (reason: unknown) => void = () => {}
    openChatSocket.mockImplementationOnce(() => new Promise<WebSocket>((_resolve, reject) => { rejectGrant = reject }))
    const authAbort = new AbortController()
    authSupport.signal = authAbort.signal
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    await screen.findByRole('textbox', { name: '消息内容' })
    fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '旧身份的问题' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    expect(openChatSocket).toHaveBeenCalledTimes(1)
    const handshakeSignal = openChatSocket.mock.calls[0][1] as AbortSignal
    authAbort.abort()
    expect(handshakeSignal.aborted).toBe(true)
    authSupport.isCurrent = () => false
    await act(async () => rejectGrant(new DOMException('Aborted', 'AbortError')))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(sessions).toHaveBeenCalledTimes(1)
    expect(FakeSocket.sockets).toHaveLength(0)
  })

  it('aborts a pending handshake when the chat page unmounts', async () => {
    openChatSocket.mockImplementationOnce(() => new Promise<WebSocket>(() => {}))
    const view = render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    fireEvent.change(await screen.findByRole('textbox', { name: '消息内容' }), { target: { value: '页面已离开' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    const handshakeSignal = openChatSocket.mock.calls[0][1] as AbortSignal
    expect(handshakeSignal.aborted).toBe(false)
    view.unmount()
    expect(handshakeSignal.aborted).toBe(true)
  })

  it('aborts a pending new-chat handshake when starting another new chat', async () => {
    openChatSocket.mockImplementationOnce(() => new Promise<WebSocket>(() => {}))
    render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage /></MemoryRouter>)
    fireEvent.change(await screen.findByRole('textbox', { name: '消息内容' }), { target: { value: '放弃这一条' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
    const handshakeSignal = openChatSocket.mock.calls[0][1] as AbortSignal
    fireEvent.click(screen.getByRole('button', { name: '新对话' }))
    expect(handshakeSignal.aborted).toBe(true)
    expect(screen.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
    expect(screen.queryByText('放弃这一条')).toBeNull()
  })
})

it('keeps phone Enter as a newline action and switches history back to a single conversation', async () => {
  const media = { matches: true, addEventListener: () => {}, removeEventListener: () => {} }
  vi.stubGlobal('matchMedia', () => media)
  sessions.mockResolvedValue({ sessions: [{ id: 'one', preview: '手机旧记录', message_count: 2, last_active_at: '2026-10-09T00:00:00Z', started_at: '2026-10-09T00:00:00Z', round_count: 1 }] })
  messages.mockResolvedValue({ id: 'one', messages: [{ role: 'assistant', content: '手机历史正文', timestamp: '2026-10-09T00:00:00Z' }] })
  render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage/></MemoryRouter>)
  const input = await screen.findByRole('textbox', { name: '消息内容' })
  fireEvent.change(input, { target: { value: '第一行' } })
  expect(fireEvent.keyDown(input, { key: 'Enter' })).toBe(true)
  expect(openChatSocket).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '查看对话列表' }))
  fireEvent.click(await screen.findByRole('button', { name: /手机旧记录/ }))
  expect(await screen.findByText('手机历史正文')).toBeInTheDocument()
  expect(screen.queryByRole('complementary', { name: '最近对话' })).not.toBeInTheDocument()
})

const controlProposal = { id: 'p-one', session_id: 'one', request_id: 'r-one', entity_id: 'light.one', name: '客厅主灯', area_name: '客厅', action: 'turn_on', status: 'pending', created_at: '2026-10-09T00:00:00Z', expires_at: '2099-10-09T00:02:00Z', before: { entity_id: 'light.one', name: '客厅主灯', state: 'off', observed_at: '2026-10-09T00:00:00Z' } }
const historyWithControl = { id: 'one', messages: [{ role: 'assistant', content: '请确认设备操作。', timestamp: '2026-10-09T00:00:00Z', attachments: [{ kind: 'control_proposal' as const, proposal_id: 'p-one' }] }] }
function reply(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }) }
it('hydrates history from current proposal state and makes exactly one Cookie/CSRF confirmation for double clicks', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  let finish: (response: Response) => void = () => {}
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  const confirm = await screen.findByRole('button', { name: '确认打开客厅主灯' })
  expect(transport.mock.calls[0][0]).toBe('/api/console/v1/control/proposals/p-one')
  fireEvent.click(confirm); fireEvent.click(confirm)
  expect(transport).toHaveBeenCalledTimes(2)
  expect(transport.mock.calls[1]).toEqual(['/api/console/v1/control/proposals/p-one/confirm', expect.objectContaining({ method: 'POST', credentials: 'same-origin', cache: 'no-store', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'memory-only' }, body: '{}' })])
  await act(async () => finish(reply({ ...controlProposal, status: 'succeeded', after: { ...controlProposal.before, state: 'on' } })))
  expect(screen.getByText('已观察到目标状态')).toBeVisible()
  expect(screen.queryByRole('button', { name: '确认打开客厅主灯' })).toBeNull()
})
it('restores unavailable history attachments with no controls and keeps old text messages readable', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  vi.mocked(fetch).mockResolvedValue(reply({ error: { code: 'not_found' } }, 404))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  expect(await screen.findByText('此设备操作记录暂不可用。')).toBeVisible()
  expect(screen.getByText('请确认设备操作。')).toBeVisible()
  expect(screen.queryByRole('button', { name: /确认打开/ })).toBeNull()
  expect(vi.mocked(fetch)).toHaveBeenCalledTimes(1)
})
it('shows only reconcile after a lost confirmation response and never automatically sends it again', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockRejectedValueOnce(new TypeError('network secret')).mockResolvedValueOnce(reply({ ...controlProposal, status: 'unknown' })).mockResolvedValueOnce(reply({ ...controlProposal, status: 'unknown', after: { ...controlProposal.before, state: 'on' } }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  const reconcile = await screen.findByRole('button', { name: '核对状态' })
  expect(screen.queryByRole('button', { name: /确认打开|取消提议/ })).toBeNull()
  expect(transport).toHaveBeenCalledTimes(2)
  fireEvent.click(reconcile)
  await screen.findByText(/核对状态：开启/)
  expect(transport.mock.calls[2][0]).toBe('/api/console/v1/control/proposals/p-one')
  expect(transport.mock.calls[3][0]).toBe('/api/console/v1/control/proposals/p-one/reconcile')
  expect(screen.queryByText(/network secret/)).toBeNull()
})
it('rejects late control HTTP results after the user switches sessions', async () => {
  capabilities.value.push('devices:control')
  messages.mockImplementation(async (id: string) => id === 'one' ? historyWithControl : { id: 'two', messages: [{ role: 'assistant', content: '另一个对话', timestamp: '2026-10-09T00:00:00Z' }] })
  sessions.mockResolvedValue({ sessions: [{ id: 'two', preview: '第二对话', started_at: '', last_active_at: '', message_count: 1, round_count: 1 }] })
  let finish: (response: Response) => void = () => {}
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  const options = transport.mock.calls[1][1]!
  fireEvent.click(screen.getByRole('button', { name: /第二对话/ }))
  await screen.findByText('另一个对话')
  expect(options.signal?.aborted).toBe(true)
  await act(async () => finish(reply({ ...controlProposal, status: 'succeeded' })))
  expect(screen.queryByText('已观察到目标状态')).toBeNull()
  expect(screen.queryByText('客厅主灯')).toBeNull()
})
it('keeps members able to send device queries and correlates a terminal read-only result to the request', async () => {
  capabilities.value.push('devices:query')
  render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage/></MemoryRouter>)
  fireEvent.change(await screen.findByRole('textbox', { name: '消息内容' }), { target: { value: '温度是多少' } })
  fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  const socket = FakeSocket.sockets[0]; socket.open()
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  const request = JSON.parse(socket.sent[0])
  expect(request.request_id).toMatch(/^[a-f0-9-]{36}$/)
  await act(async () => socket.receive({ type: 'session', session_id: 'one' }))
  await act(async () => socket.receive({ type: 'device_result', session_id: 'other', request_id: request.request_id, device_result: { entity_id: 'sensor.temp', name: '错误对话的温度', state: '99', observed_at: '2026-10-09' } }))
  expect(screen.queryByText('错误对话的温度')).toBeNull()
  await act(async () => socket.receive({ type: 'device_result', session_id: 'one', request_id: request.request_id, device_result: { entity_id: 'sensor.temp', name: '客厅温度', state: '24', observed_at: '2026-10-09T00:00:00Z' } }))
  expect(await screen.findByRole('region', { name: '客厅温度设备状态' })).toHaveTextContent('24')
  expect(screen.queryByRole('button', { name: /确认打开/ })).toBeNull()
  expect(screen.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
  expect(socket.closed).toBe(true)
})

it('deduplicates confirmation across repeated history references to the same proposal', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue({ ...historyWithControl, messages: [...historyWithControl.messages, { ...historyWithControl.messages[0], timestamp: '2026-10-09T00:00:01Z' }] })
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockImplementation(() => new Promise<Response>(() => {}))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  await screen.findAllByRole('button', { name: '确认打开客厅主灯' })
  const buttons = screen.getAllByRole('button', { name: '确认打开客厅主灯' })
  fireEvent.click(buttons[0]); fireEvent.click(buttons[1])
  expect(transport.mock.calls.filter(([url]) => String(url).endsWith('/confirm'))).toHaveLength(1)
})
it('clears control cards on identity changes and discards a late confirmation from the previous account', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  let finish: (response: Response) => void = () => {}
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve }))
  const view = render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  const oldSignal = transport.mock.calls[1][1]?.signal
  authSupport.epoch = 2; authSupport.userId = 'bob'; authSupport.isCurrent = (epoch) => epoch === 2
  capabilities.value = ['chat:use', 'sessions:read', 'devices:query']
  view.rerender(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  expect(screen.queryByText('客厅主灯')).toBeNull()
  expect(oldSignal?.aborted).toBe(true)
  await act(async () => finish(reply({ ...controlProposal, status: 'succeeded' })))
  expect(screen.queryByText('已观察到目标状态')).toBeNull()
})
it('uses a terminal proposal frame without writing until the independent confirm button is pressed', async () => {
  capabilities.value.push('devices:control')
  render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage/></MemoryRouter>)
  fireEvent.change(await screen.findByRole('textbox', { name: '消息内容' }), { target: { value: '打开客厅主灯' } })
  fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  const socket = FakeSocket.sockets[0]; socket.open()
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  const request = JSON.parse(socket.sent[0])
  await act(async () => socket.receive({ type: 'session', session_id: 'one', request_id: request.request_id }))
  await act(async () => socket.receive({ type: 'control_proposal', session_id: 'one', request_id: request.request_id, content: '安全提议说明', proposal: { ...controlProposal, request_id: request.request_id } }))
  expect(await screen.findByRole('button', { name: '确认打开客厅主灯' })).toBeEnabled()
  expect(screen.getByText('安全提议说明')).toBeVisible()
  expect(vi.mocked(fetch)).not.toHaveBeenCalled()
  fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '确认' } })
  fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  expect(vi.mocked(fetch)).not.toHaveBeenCalled()
  const followup = FakeSocket.sockets[1]; followup.open()
  await waitFor(() => expect(followup.sent).toHaveLength(1))
  expect(JSON.parse(followup.sent[0]).request_id).not.toBe(request.request_id)
})
it('reads authoritative executing status after a rejected cancellation and never reports withdrawal', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockResolvedValueOnce(reply({ error: { code: 'control_conflict' } }, 409)).mockResolvedValueOnce(reply({ ...controlProposal, status: 'executing' }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '取消提议' }))
  expect(await screen.findByText('正在执行，无法撤回')).toBeVisible()
  expect(screen.queryByText(/已取消|已撤回/)).toBeNull()
  expect(screen.queryByRole('button', { name: '取消提议' })).toBeNull()
})
it('rejects a restored proposal whose server session differs from the attachment session', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  vi.mocked(fetch).mockResolvedValueOnce(reply({ ...controlProposal, session_id: 'other' }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  expect(await screen.findByText('此设备操作记录暂不可用。')).toBeVisible()
  expect(screen.queryByRole('button', { name: /确认打开/ })).toBeNull()
})
it('handles a malformed null WebSocket frame as uncertain without crashing or replaying', async () => {
  render(<MemoryRouter initialEntries={['/app/chat']}><ChatPage/></MemoryRouter>)
  fireEvent.change(await screen.findByRole('textbox', { name: '消息内容' }), { target: { value: '打开灯' } })
  fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  const socket = FakeSocket.sockets[0]; socket.open()
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  await act(async () => socket.onmessage?.({ data: 'null' } as MessageEvent))
  expect(screen.getByRole('alert')).toHaveTextContent('重发可能产生重复')
  expect(FakeSocket.sockets).toHaveLength(1)
})

it.each(['succeeded', 'failed', 'pending', 'executing'] as const)('reads current %s state after a lost confirmation before attempting reconcile', async (status) => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply(controlProposal)).mockRejectedValueOnce(new TypeError('lost response'))
  transport.mockImplementation(async (_url, options) => options?.method === 'POST'
    ? reply({ error: { code: 'control_conflict' } }, 409)
    : reply({ ...controlProposal, status }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  fireEvent.click(await screen.findByRole('button', { name: '核对状态' }))
  const statusMessages = { succeeded: '已观察到目标状态', failed: '操作未完成，请重新提出需求', pending: '等待确认', executing: '正在执行，无法撤回' }
  await screen.findByText(statusMessages[status])
  expect(transport.mock.calls.filter(([url]) => String(url).endsWith('/reconcile'))).toHaveLength(0)
  expect(transport.mock.calls.filter(([url]) => String(url).endsWith('/confirm'))).toHaveLength(1)
  if (status === 'pending') expect(screen.getByRole('button', { name: '确认打开客厅主灯' })).toBeEnabled()
  else expect(screen.queryByRole('button', { name: /确认打开|取消提议/ })).toBeNull()
})

it('refreshes current proposal when reconcile races a server status change and receives conflict', async () => {
  capabilities.value.push('devices:control')
  messages.mockResolvedValue(historyWithControl)
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply({ ...controlProposal, status: 'unknown' }))
    .mockResolvedValueOnce(reply({ ...controlProposal, status: 'unknown' }))
    .mockResolvedValueOnce(reply({ error: { code: 'control_conflict' } }, 409))
    .mockResolvedValueOnce(reply({ ...controlProposal, status: 'succeeded' }))
  render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '核对状态' }))
  expect(await screen.findByText('已观察到目标状态')).toBeVisible()
  expect(transport.mock.calls.map(([url]) => url)).toEqual([
    '/api/console/v1/control/proposals/p-one', '/api/console/v1/control/proposals/p-one',
    '/api/console/v1/control/proposals/p-one/reconcile', '/api/console/v1/control/proposals/p-one',
  ])
})
it.each(['session', 'identity'] as const)('discards a late reconciliation GET after switching %s, without sending a reconcile POST', async (change) => {
  capabilities.value.push('devices:control')
  messages.mockImplementation(async (id: string) => id === 'one' ? historyWithControl : { id: 'two', messages: [] })
  sessions.mockResolvedValue({ sessions: [{ id: 'two', preview: '第二对话', started_at: '2026-10-09', last_active_at: '2026-10-09', message_count: 0, round_count: 0 }] })
  let finish: (response: Response) => void = () => {}
  const transport = vi.mocked(fetch)
  transport.mockResolvedValueOnce(reply({ ...controlProposal, status: 'unknown' })).mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve }))
  const view = render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '核对状态' }))
  const oldSignal = transport.mock.calls[1][1]?.signal
  if (change === 'session') fireEvent.click(await screen.findByRole('button', { name: /第二对话/ }))
  else {
    authSupport.epoch = 2; authSupport.userId = 'bob'; authSupport.isCurrent = (epoch) => epoch === 2
    view.rerender(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage/></MemoryRouter>)
  }
  expect(oldSignal?.aborted).toBe(true)
  await act(async () => finish(reply({ ...controlProposal, status: 'unknown' })))
  expect(transport.mock.calls.filter(([url]) => String(url).endsWith('/reconcile'))).toHaveLength(0)
  expect(screen.queryByText('客厅主灯')).toBeNull()
})
