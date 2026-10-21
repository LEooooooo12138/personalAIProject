import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'

const settings = { revision: 's1', updated_at: '', key_configured: true, llm_mode: 'local', asr_mode: 'local', tts_mode: 'cloud', local_asr_url: 'http://127.0.0.1:8000/asr', local_tts_url: 'http://127.0.0.1:8000/tts' }
const missing = { exists: false, size_bytes: 0, updated_at: '', revision: 'v0' }
const stored = { exists: true, size_bytes: 48, updated_at: '2026-10-13T10:00:00Z', revision: 'v1' }
const actor = { user: { id: 'a1', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }, capabilities: ['providers:manage'], csrf_token: 'csrf-1' }
type Write = { path: string; method: string; body: Record<string, unknown>; headers: HeadersInit | undefined }
afterEach(() => vi.unstubAllGlobals())
function setup(options: { settings?: object; reference?: typeof missing; read?: () => Promise<typeof missing>; write?: (entry: Write) => Promise<Response> } = {}) {
  const writes: Write[] = []
  let reference = options.reference ?? missing
  vi.stubGlobal('fetch', vi.fn(async (url: string, request?: RequestInit) => {
    if (url.endsWith('/auth/me')) return Response.json(actor)
    if (request?.method === 'PUT' || request?.method === 'POST') {
      const entry = { path: url, method: request.method, body: JSON.parse(request.body as string), headers: request.headers }
      writes.push(entry)
      if (options.write) return options.write(entry)
      if (url.endsWith('/tts-reference')) { reference = stored; return Response.json(reference) }
      if (url.endsWith('/tts-reference/remove')) { reference = { ...missing, revision: 'v2' }; return Response.json(reference) }
      return Response.json({ ...settings, ...entry.body, revision: 's2' })
    }
    if (url.endsWith('/assistant')) return Response.json({ ...settings, ...options.settings })
    if (url.endsWith('/tts-reference')) return Response.json(options.read ? await options.read() : reference)
    return Response.json({})
  }))
  const view = render(<MemoryRouter initialEntries={['/app/admin/assistant']}><App/></MemoryRouter>)
  return { ...view, writes }
}
function audioFile(name = 'voice.wav', read = async () => new Uint8Array([82, 73, 70, 70]).buffer) {
  const file = new File(['RIFF'], name, { type: name.endsWith('.mp3') ? 'audio/mpeg' : 'audio/wav' })
  Object.defineProperty(file, 'arrayBuffer', { value: read })
  return file
}
async function selectAndUpload(file = audioFile()) {
  fireEvent.change(await screen.findByLabelText('克隆参考音频'), { target: { files: [file] } })
  const upload = screen.getByRole('button', { name: /^(上传|替换)参考音频$/ })
  await waitFor(() => expect(upload).toBeEnabled())
  fireEvent.click(upload)
}

it('saves legacy settings with the default MiMo model and voice without changing local addresses', async () => {
  const { writes } = setup()
  expect(await screen.findByLabelText('MiMo 朗读模型')).toHaveValue('mimo-v2.5-tts')
  expect(screen.getByLabelText('MiMo 内置音色')).toHaveValue('冰糖')
  fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body).toMatchObject({ tts_model: 'mimo-v2.5-tts', tts_voice: '冰糖', local_tts_url: 'http://127.0.0.1:8000/tts' })
})

it('preserves a selected builtin voice and local address while switching clone and local modes', async () => {
  const { writes } = setup()
  fireEvent.change(await screen.findByLabelText('MiMo 内置音色'), { target: { value: 'Mia' } })
  fireEvent.change(screen.getByLabelText('MiMo 朗读模型'), { target: { value: 'mimo-v2.5-tts-voiceclone' } })
  expect(await screen.findByText(/使用 MiMo 克隆朗读前，请先上传参考音频/)).toBeInTheDocument()
  expect(screen.getByLabelText('MiMo 内置音色')).toBeDisabled()
  fireEvent.change(screen.getByLabelText('TTS 模式'), { target: { value: 'local' } })
  expect(screen.getByLabelText('本地 TTS 服务地址')).toHaveValue('http://127.0.0.1:8000/tts')
  fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body).toMatchObject({ tts_mode: 'local', tts_model: 'mimo-v2.5-tts-voiceclone', tts_voice: 'Mia' })
})

it('uploads a selected sample only to the reference endpoint with its revision and CSRF token', async () => {
  const { writes } = setup()
  const input = await screen.findByLabelText('克隆参考音频')
  fireEvent.change(input, { target: { files: [audioFile('voice.mp3')] } })
  expect(writes).toHaveLength(0)
  fireEvent.click(await screen.findByRole('button', { name: '上传参考音频' }))
  await screen.findByText('参考音频已保存。上传未调用 MiMo。')
  expect(writes).toHaveLength(1)
  expect(writes[0]).toMatchObject({ path: '/api/console/v1/admin/tts-reference', method: 'PUT', body: { filename: 'voice.mp3', audio_base64: 'UklGRg==', expected_revision: 'v0' }, headers: { 'X-CSRF-Token': 'csrf-1' } })
  expect(input).toHaveValue('')
  expect(screen.getByRole('button', { name: '移除参考音频' })).toBeEnabled()
})

it.each([
  ['voice.ogg', 4, /请选择 WAV 或 MP3/],
  ['voice.wav', 0, /参考音频不能为空/],
  ['voice.wav', 6 * 1024 * 1024 + 1, /参考音频不能超过 6 MiB/],
])('rejects invalid reference input before reading or sending: %s (%s bytes)', async (name, size, message) => {
  const { writes } = setup()
  const read = vi.fn(async () => new ArrayBuffer(4))
  const file = audioFile(name, read)
  Object.defineProperty(file, 'size', { value: size })
  await selectAndUpload(file)
  expect(await screen.findByText(message)).toBeInTheDocument()
  expect(writes).toHaveLength(0)
  expect(read).not.toHaveBeenCalled()
})

it('waits for reference metadata before uploading a selected sample', async () => {
  let resolve!: (value: typeof missing) => void
  const read = new Promise<typeof missing>(done => { resolve = done })
  const { writes } = setup({ read: () => read })
  fireEvent.change(await screen.findByLabelText('克隆参考音频'), { target: { files: [audioFile()] } })
  expect(screen.getByRole('button', { name: '上传参考音频' })).toBeDisabled()
  await act(async () => resolve({ ...missing, revision: 'v9' }))
  fireEvent.click(screen.getByRole('button', { name: '上传参考音频' }))
  await waitFor(() => expect(writes[0]?.body.expected_revision).toBe('v9'))
})

it('cancels a pending file read without uploading its late bytes', async () => {
  let resolve!: (value: ArrayBuffer) => void
  const { writes } = setup()
  await selectAndUpload(audioFile('voice.wav', () => new Promise(done => { resolve = done })))
  fireEvent.click(screen.getByRole('button', { name: '取消读取' }))
  await act(async () => resolve(new Uint8Array([82, 73, 70, 70]).buffer))
  expect(writes).toHaveLength(0)
  expect(screen.getByRole('button', { name: '上传参考音频' })).toBeDisabled()
})

it('does not upload a file read that completes after leaving the page', async () => {
  let resolve!: (value: ArrayBuffer) => void
  const { writes, unmount } = setup()
  await selectAndUpload(audioFile('voice.wav', () => new Promise(done => { resolve = done })))
  unmount()
  await act(async () => resolve(new Uint8Array([82, 73, 70, 70]).buffer))
  expect(writes).toHaveLength(0)
})

it('does not duplicate a pending reference write or apply its late response after the identity changes', async () => {
  let resolve!: (value: Response) => void
  const { writes } = setup({ write: () => new Promise(done => { resolve = done }) })
  await selectAndUpload()
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(screen.getByLabelText('克隆参考音频')).toBeDisabled()
  expect(screen.queryByRole('button', { name: '取消读取' })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '正在保存参考音频…' }))
  expect(writes).toHaveLength(1)
  const original = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (url, options) => String(url).endsWith('/auth/me') ? Response.json({ ...actor, user: { ...actor.user, id: 'a2', display_name: '新身份' }, capabilities: [], csrf_token: 'csrf-2' }) : original(url, options))
  fireEvent.focus(window)
  await screen.findByText('无权管理助手设置')
  await act(async () => resolve(Response.json(stored)))
  expect(screen.queryByText('参考音频已保存。上传未调用 MiMo。')).toBeNull()
  expect(screen.queryByLabelText('克隆参考音频')).toBeNull()
})

it('ignores reference metadata returned for a previous identity and uses the new revision for upload', async () => {
  let resolve!: (value: typeof missing) => void
  let reads = 0
  const { writes } = setup({ read: () => ++reads === 1 ? new Promise(done => { resolve = done }) : Promise.resolve({ ...missing, revision: 'new-user-v1' }) })
  await screen.findByLabelText('克隆参考音频')
  await waitFor(() => expect(reads).toBe(1))
  const original = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (url, options) => String(url).endsWith('/auth/me') ? Response.json({ ...actor, user: { ...actor.user, id: 'a2' }, csrf_token: 'csrf-2' }) : original(url, options))
  fireEvent.focus(window)
  await waitFor(() => expect(reads).toBe(2))
  await act(async () => resolve({ ...stored, revision: 'old-user-v9' }))
  await selectAndUpload()
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body.expected_revision).toBe('new-user-v1')
  expect(writes[0].headers).toMatchObject({ 'X-CSRF-Token': 'csrf-2' })
})

it('removes only the current reference revision and shows the unconfigured state', async () => {
  const { writes } = setup({ reference: stored })
  fireEvent.click(await screen.findByRole('button', { name: '移除参考音频' }))
  await screen.findByText('参考音频已移除。')
  expect(writes).toHaveLength(1)
  expect(writes[0]).toMatchObject({ path: '/api/console/v1/admin/tts-reference/remove', method: 'POST', body: { expected_revision: 'v1' } })
  expect(screen.queryByRole('button', { name: '移除参考音频' })).not.toBeInTheDocument()
})

it('preserves a conflicted upload for an explicit retry after refreshing its reference revision', async () => {
  let revision = 'v0'
  let conflict = true
  const { writes } = setup({ read: async () => ({ ...missing, revision }), write: async () => conflict ? Response.json({ error: { code: 'revision_conflict' } }, { status: 409 }) : Response.json(stored) })
  await selectAndUpload()
  expect(await screen.findByRole('alert')).toHaveTextContent('重新读取后保存')
  expect(writes).toHaveLength(1)
  expect(screen.queryByText('参考音频已保存。上传未调用 MiMo。')).not.toBeInTheDocument()
  revision = 'v8'; conflict = false
  const refresh = screen.getByRole('button', { name: '重新读取参考音频状态' })
  await waitFor(() => expect(refresh).toBeEnabled())
  fireEvent.click(refresh)
  const button = screen.getByRole('button', { name: '上传参考音频' })
  await waitFor(() => expect(button).toBeEnabled())
  fireEvent.click(button)
  await waitFor(() => expect(writes).toHaveLength(2))
  expect(writes[1].body.expected_revision).toBe('v8')
})
