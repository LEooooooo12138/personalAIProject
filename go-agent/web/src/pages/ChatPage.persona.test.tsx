import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import ChatPage from './ChatPage'
import type { ConsoleMessage } from '../api'

const { messages, sessions, openChatSocket, voice, voiceTranscript, auth } = vi.hoisted(() => ({
  messages: vi.fn(), sessions: vi.fn(), openChatSocket: vi.fn(),
  voiceTranscript: { current: (_text: string) => {} },
  voice: { available: true, phase: 'idle', cancel: vi.fn(), onFinal: vi.fn(), speak: vi.fn() },
  auth: { epoch: 1, userId: 'alice', signal: new AbortController().signal },
}))
vi.mock('../api', async (original) => ({ ...(await original<typeof import('../api')>()), consoleApi: { messages, sessions, me: vi.fn() } }))
vi.mock('../auth', () => ({ useAuth: () => ({ identity: { user: { id: auth.userId }, csrf_token: 'csrf', capabilities: ['chat:use', 'sessions:read', 'devices:control'] }, epoch: auth.epoch, signal: auth.signal, isCurrent: (epoch: number) => epoch === auth.epoch, expire: vi.fn() }) }))
vi.mock('../coordinator', () => ({ openChatSocket, coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options) }))
vi.mock('../chat/useVoice', () => ({ useVoice: (_sid: unknown, _revision: unknown, _enabled: unknown, onTranscript: (text: string) => void) => { voiceTranscript.current = onTranscript; return voice } }))
vi.mock('../chat/VoiceControls', () => ({ default: () => null }))
class Socket {
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  sent: string[] = []
  send(text: string) { this.sent.push(text) }
  close() { this.onclose?.() }
  receive(frame: object) { this.onmessage?.({ data: JSON.stringify(frame) } as MessageEvent) }
}
const proposal = { id: 'p', session_id: 'one', request_id: 'r', entity_id: 'light.one', name: '客厅主灯', area_name: '客厅', action: 'turn_on', status: 'pending', created_at: '', expires_at: '2099-01-01' }
const opening: ConsoleMessage = { role: 'assistant', content: '请核对这盏灯。', timestamp: '2026-10-12', attachments: [{ kind: 'control_proposal', proposal_id: 'p' }] }
const terminal = { role: 'assistant', content: '好呀，已读取到客厅主灯的目标状态。', timestamp: '2026-10-12T00:00:01Z', event_id: 'terminal-p', personality_revision: 'persona-v1', presentation_version: 'v1', attachments: [{ kind: 'control_proposal', proposal_id: 'p' }] }
const response = (body: unknown) => Response.json(body)
const result = (status = 'succeeded', index = 1) => ({ ...proposal, status, assistant_message: { index, message: terminal } })
function mount() { return render(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage /></MemoryRouter>) }
beforeEach(() => {
  voice.available = true
  messages.mockReset(); messages.mockResolvedValue({ id: 'one', messages: [opening] })
  sessions.mockReset(); sessions.mockResolvedValue({ sessions: [] })
  openChatSocket.mockReset(); Object.values(voice).forEach((value) => { if (typeof value === 'function') value.mockClear() })
  auth.epoch = 1; auth.userId = 'alice'; auth.signal = new AbortController().signal
  vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => response(proposal)))
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }))
})
it.each([['confirm', '确认打开客厅主灯', 'succeeded'], ['cancel', '取消提议', 'cancelled']])('appends %s stored reply and speaks its exact server index once', async (_operation, label, status) => {
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementation(async () => response(result(status)))
  mount()
  fireEvent.click(await screen.findByRole('button', { name: label }))
  expect(await screen.findByText(terminal.content)).toBeVisible()
  expect(voice.onFinal).toHaveBeenCalledExactlyOnceWith('one', 1, terminal.content)
  fireEvent.click(screen.getAllByRole('button', { name: '朗读回答' })[1])
  expect(voice.speak).toHaveBeenCalledWith('one', 1, terminal.content)
})
it('restores reconcile text from GET without repeating a settled control mutation', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(response({ ...proposal, status: 'unknown' })).mockImplementation(async () => response(result()))
  mount()
  fireEvent.click(await screen.findByRole('button', { name: '核对状态' }))
  expect(await screen.findByText(terminal.content)).toBeVisible()
  expect(vi.mocked(fetch).mock.calls.every(([, options]) => (options?.method ?? 'GET') === 'GET')).toBe(true)
  expect(voice.onFinal).toHaveBeenCalledExactlyOnceWith('one', 1, terminal.content)
})
it('does not replay or auto-read a result already present after refresh', async () => {
  messages.mockResolvedValue({ id: 'one', messages: [opening, terminal] })
  vi.mocked(fetch).mockImplementation(async () => response(result()))
  mount()
  expect(await screen.findByText(terminal.content)).toBeVisible()
  await waitFor(() => expect(fetch).toHaveBeenCalled())
  expect(screen.getAllByText(terminal.content)).toHaveLength(1)
  expect(voice.onFinal).not.toHaveBeenCalled()
})
it('updates a legacy bare proposal without inventing a reply', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementation(async () => response({ ...proposal, status: 'succeeded' }))
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  expect(await screen.findByText('已观察到目标状态')).toBeVisible()
  expect(screen.queryByText(terminal.content)).toBeNull(); expect(voice.onFinal).not.toHaveBeenCalled()
})
it('keeps an interleaved user turn and recovers order only after its response', async () => {
  let finish: (value: Response) => void = () => {}
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve })).mockImplementation(async () => response(result('succeeded', 2)))
  const socket = new Socket(); openChatSocket.mockResolvedValue(socket)
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  const input = screen.getByRole('textbox', { name: '消息内容' })
  fireEvent.change(input, { target: { value: '另一个问题' } }); fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  await act(async () => finish(response(result('succeeded', 2))))
  expect(input).toBeDisabled(); expect(screen.getByText('另一个问题')).toBeVisible()
  expect(screen.queryByText(terminal.content)).toBeNull(); expect(messages).toHaveBeenCalledTimes(1)
  messages.mockResolvedValue({ id: 'one', messages: [opening, { role: 'user', content: '另一个问题', timestamp: '' }, terminal, { role: 'assistant', content: '聊天回复', timestamp: '' }] })
  await act(async () => socket.receive({ type: 'response', content: '聊天回复' }))
  expect(await screen.findByText(terminal.content)).toBeVisible()
  expect(voice.onFinal).not.toHaveBeenCalled()
  fireEvent.click(screen.getAllByRole('button', { name: '朗读回答' })[2])
  expect(voice.speak).toHaveBeenCalledWith('one', 3, '聊天回复')
})
it('ignores a late result after an identity change', async () => {
  let finish: (value: Response) => void = () => {}
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve }))
  const view = mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  auth.epoch = 2; auth.userId = 'bob'; messages.mockResolvedValue({ id: 'one', messages: [] })
  view.rerender(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage /></MemoryRouter>)
  await act(async () => finish(response(result())))
  expect(screen.queryByText(terminal.content)).toBeNull(); expect(voice.onFinal).not.toHaveBeenCalled()
})
it('does not auto-read a chat response that arrives before an overlapping control response', async () => {
  let finish: (value: Response) => void = () => {}
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve })).mockImplementation(async () => response(result('succeeded', 2)))
  const socket = new Socket(); openChatSocket.mockResolvedValue(socket)
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '第二个问题' } }); fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  await act(async () => socket.receive({ type: 'response', content: '聊天回复' }))
  expect(voice.onFinal).not.toHaveBeenCalled()
  expect(screen.getAllByRole('button', { name: '朗读回答' })[1]).toBeDisabled()
  expect(messages).toHaveBeenCalledTimes(1)
  messages.mockResolvedValue({ id: 'one', messages: [opening, { role: 'user', content: '第二个问题', timestamp: '' }, terminal, { role: 'assistant', content: '聊天回复', timestamp: '' }] })
  await act(async () => finish(response(result('succeeded', 2))))
  expect(await screen.findByText(terminal.content)).toBeVisible()
  fireEvent.click(screen.getAllByRole('button', { name: '朗读回答' })[2])
  expect(voice.speak).toHaveBeenCalledWith('one', 3, '聊天回复')
})
it('ignores a late control result after selecting a new session', async () => {
  let finish: (value: Response) => void = () => {}
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve }))
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  fireEvent.click(screen.getByRole('button', { name: '新对话' }))
  await act(async () => finish(response(result())))
  expect(screen.queryByText(terminal.content)).toBeNull(); expect(voice.onFinal).not.toHaveBeenCalled()
})
it('does not replace a newer completed turn with a delayed recovery history', async () => {
  let history: (value: unknown) => void = () => {}
  messages.mockResolvedValueOnce({ id: 'one', messages: [opening] }).mockImplementationOnce(() => new Promise((resolve) => { history = resolve }))
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementation(async () => response(result('succeeded', 2)))
  const socket = new Socket(); openChatSocket.mockResolvedValue(socket)
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  await waitFor(() => expect(messages).toHaveBeenCalledTimes(2))
  fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '较新的问题' } }); fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  messages.mockResolvedValue({ id: 'one', messages: [opening, { role: 'user', content: '较新的问题', timestamp: '' }, { role: 'assistant', content: '较新的回答', timestamp: '' }] })
  await act(async () => socket.receive({ type: 'response', content: '较新的回答' }))
  await act(async () => history({ id: 'one', messages: [opening, terminal] }))
  expect(screen.getByText('较新的问题')).toBeVisible(); expect(screen.getByText('较新的回答')).toBeVisible()
  expect(screen.queryByText(terminal.content)).toBeNull(); expect(voice.onFinal).not.toHaveBeenCalled()
})
it('defers speech while an executing proposal is being refreshed during chat', async () => {
  let refresh: (value: Response) => void = () => {}
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockResolvedValueOnce(response({ ...proposal, status: 'executing' })).mockImplementationOnce(() => new Promise((resolve) => { refresh = resolve })).mockImplementation(async () => response(result('succeeded', 2)))
  const socket = new Socket(); openChatSocket.mockResolvedValue(socket)
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3))
  fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '同时聊天' } }); fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  messages.mockResolvedValue({ id: 'one', messages: [opening, { role: 'user', content: '同时聊天', timestamp: '' }, terminal, { role: 'assistant', content: '普通回复', timestamp: '' }] })
  await act(async () => socket.receive({ type: 'response', content: '普通回复' }))
  expect(voice.onFinal).not.toHaveBeenCalled()
  await act(async () => refresh(response(result('succeeded', 2))))
  expect(await screen.findByText(terminal.content)).toBeVisible()
  expect(voice.onFinal).not.toHaveBeenCalled()
})
it('offers a bounded read-only retry after result-history recovery fails', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementation(async () => response(result('succeeded', 2)))
  messages.mockResolvedValueOnce({ id: 'one', messages: [opening] }).mockRejectedValueOnce(new TypeError('offline'))
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  const retry = await screen.findByRole('button', { name: '重新读取对话' })
  expect(messages).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('button', { name: '朗读回答' })).toBeDisabled()
  messages.mockResolvedValue({ id: 'one', messages: [opening, { role: 'user', content: '另一页面的消息', timestamp: '' }, terminal] })
  fireEvent.click(retry)
  expect(await screen.findByText(terminal.content)).toBeVisible()
  expect(vi.mocked(fetch).mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1)
  expect(voice.onFinal).not.toHaveBeenCalled()
})
it('accepts a newer GET reply at the exact tail after recovery without autoplay', async () => {
  messages.mockResolvedValueOnce({ id: 'one', messages: [opening] }).mockResolvedValue({ id: 'one', messages: [opening, { role: 'user', content: '其他页面已保存的问题', timestamp: '' }] })
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementation(async () => response(result('succeeded', 2)))
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  expect(await screen.findByText(terminal.content)).toBeVisible()
  expect(voice.onFinal).not.toHaveBeenCalled()
  fireEvent.click(screen.getAllByRole('button', { name: '朗读回答' })[1])
  expect(voice.speak).toHaveBeenCalledWith('one', 2, terminal.content)
})
it('keeps speech disabled when recovery and GET indexes still disagree without looping reads', async () => {
  vi.mocked(fetch).mockResolvedValueOnce(response(proposal)).mockImplementation(async () => response(result('succeeded', 5)))
  mount(); fireEvent.click(await screen.findByRole('button', { name: '确认打开客厅主灯' }))
  expect(await screen.findByRole('button', { name: '重新读取对话' })).toBeVisible()
  expect(messages).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('button', { name: '朗读回答' })).toBeDisabled()
  expect(screen.queryByText(terminal.content)).toBeNull(); expect(voice.onFinal).not.toHaveBeenCalled()
})
it('recovers exact indexes when the initial history proposal GET persists a result before chat starts', async () => {
  let proposalGet: (value: Response) => void = () => {}
  vi.mocked(fetch).mockImplementationOnce(() => new Promise((resolve) => { proposalGet = resolve })).mockImplementation(async () => response(result()))
  const socket = new Socket(); openChatSocket.mockResolvedValue(socket)
  mount()
  expect(await screen.findByText(opening.content)).toBeVisible()
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
  fireEvent.change(screen.getByRole('textbox', { name: '消息内容' }), { target: { value: '恢复途中发送的问题' } }); fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  messages.mockResolvedValue({ id: 'one', messages: [opening, terminal, { role: 'user', content: '恢复途中发送的问题', timestamp: '' }, { role: 'assistant', content: '位于索引三的回答', timestamp: '' }] })
  await act(async () => proposalGet(response(result())))
  expect(screen.queryByText(terminal.content)).toBeNull()
  await act(async () => socket.receive({ type: 'response', content: '位于索引三的回答' }))
  expect(voice.onFinal).not.toHaveBeenCalled()
  expect(await screen.findByText(terminal.content)).toBeVisible()
  fireEvent.click(screen.getAllByRole('button', { name: '朗读回答' })[2])
  expect(voice.speak).toHaveBeenCalledWith('one', 3, '位于索引三的回答')
})
it('renders one control card while preserving the initial and terminal assistant messages', async () => {
  messages.mockResolvedValue({ id: 'one', messages: [opening, terminal] })
  vi.mocked(fetch).mockImplementation(async () => response(result()))
  mount()
  expect(await screen.findByText(opening.content)).toBeVisible()
  expect(await screen.findByText(terminal.content)).toBeVisible()
  await waitFor(() => expect(screen.getAllByRole('region', { name: '客厅主灯控制提议' })).toHaveLength(1))
  expect(screen.getAllByRole('button', { name: '朗读回答' })).toHaveLength(2)
})

it('disables manual reading while voice configuration is untrusted and restores it after a successful read', async () => {
 voice.available = false
 const view = mount()
 expect(await screen.findByText(opening.content)).toBeVisible()
 const reading = screen.getByRole('button', { name: '朗读回答' })
 expect(reading).toBeDisabled()
 fireEvent.click(reading)
 expect(voice.speak).not.toHaveBeenCalled()
 voice.available = true
 view.rerender(<MemoryRouter initialEntries={['/app/chat?sid=one']}><ChatPage /></MemoryRouter>)
 expect(reading).toBeEnabled()
 fireEvent.click(reading)
 expect(voice.speak).toHaveBeenCalledExactlyOnceWith('one', 0, opening.content)
})


it('sends voice transcription through the selected chat entry without inventing a second send path', async () => {
  const socket = new Socket(); openChatSocket.mockResolvedValue(socket)
  mount(); await screen.findByRole('button', { name: '确认打开客厅主灯' })
  fireEvent.change(screen.getByRole('combobox', { name: '聊天入口' }), { target: { value: 'chat' } })
  await act(async () => voiceTranscript.current('语音问题'))
  await waitFor(() => expect(socket.sent).toHaveLength(1))
  expect(JSON.parse(socket.sent[0])).toMatchObject({ content: '语音问题', entry: 'chat', session_id: 'one', request_id: expect.any(String) })
  expect(screen.getByRole('combobox', { name: '聊天入口' })).toBeDisabled()
  await act(async () => socket.receive({ type: 'response', content: '普通语音回答' }))
  expect(voice.onFinal).toHaveBeenCalledExactlyOnceWith('one', 2, '普通语音回答')
})
