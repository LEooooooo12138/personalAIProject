import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'

const settings = { revision: 'r1', updated_at: '', key_configured: false, llm_mode: 'local', asr_mode: 'local', tts_mode: 'local', local_asr_url: '', local_tts_url: '' }
const actor = { user: { id: 'a1', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }, capabilities: ['providers:manage', 'personality:manage'], csrf_token: 'csrf-1' }
afterEach(() => vi.unstubAllGlobals())
function setup(capabilities = actor.capabilities, personalityReady: Promise<void> = Promise.resolve()) {
  const writes: { path: string; body: Record<string, unknown>; headers: HeadersInit | undefined }[] = []
  vi.stubGlobal('fetch', vi.fn(async (path: string, options?: RequestInit) => {
    if (path.endsWith('/auth/me')) return Response.json({ ...actor, capabilities })
    if (options?.method === 'PUT') {
      const body = JSON.parse(options.body as string); writes.push({ path, body, headers: options.headers })
      return Response.json(path.endsWith('/personality') ? { exists: true, size_bytes: 12, revision: 'p2', updated_at: '' } : { ...settings, ...body, api_key: undefined, revision: 'r2', key_configured: true })
    }
    if (path.endsWith('/assistant')) return Response.json(settings)
    if (path.endsWith('/personality')) { await personalityReady; return Response.json({ exists: false, size_bytes: 0, revision: 'p1', updated_at: '' }) }
    return Response.json({})
  }))
  render(<MemoryRouter initialEntries={['/app/admin/assistant']}><App/></MemoryRouter>)
  return writes
}
async function uploadSelectedFile() {
  const button = screen.getByRole('button', { name: '上传人格文件' })
  await waitFor(() => expect(button).toBeEnabled())
  fireEvent.click(button)
}
it('saves independent modes and a transient key, clearing the secret after saving', async () => {
  const writes = setup()
  fireEvent.change(await screen.findByLabelText('MiMo API Key'), { target: { value: 'secret-value' } })
  fireEvent.change(screen.getByLabelText('LLM 模式'), { target: { value: 'hybrid' } })
  fireEvent.change(screen.getByLabelText('ASR 模式'), { target: { value: 'cloud' } })
  fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body).toMatchObject({ expected_revision: 'r1', api_key: 'secret-value', llm_mode: 'hybrid', asr_mode: 'cloud', tts_mode: 'local' })
  expect(writes[0].headers).toMatchObject({ 'X-CSRF-Token': 'csrf-1' })
  await waitFor(() => expect(screen.getByLabelText('MiMo API Key')).toHaveValue(''))
  expect(localStorage.getItem('api_key')).toBeNull()
})
it('uploads only a selected markdown file and rejects oversized input before saving', async () => {
  const writes = setup()
  const input = await screen.findByLabelText('人格 Markdown 文件')
  const file = new File(['# 家序'], 'character.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new TextEncoder().encode('# 家序').buffer })
  fireEvent.change(input, { target: { files: [file] } })
  await uploadSelectedFile()
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body).toEqual({ filename: 'character.md', content: '# 家序', expected_revision: 'p1' })
  const large = new File([new Uint8Array(256 * 1024 + 1)], 'large.md')
  await waitFor(() => expect(input).toBeEnabled())
  fireEvent.change(input, { target: { files: [large] } })
  await uploadSelectedFile()
  expect(await screen.findByText(/不能超过 256 KiB/)).toBeInTheDocument()
  expect(writes).toHaveLength(1)
})
it('denies assistant settings when the admin lacks both capabilities', async () => {
  setup([])
  expect(await screen.findByText('无权管理助手设置')).toBeInTheDocument()
  expect(screen.queryByLabelText('MiMo API Key')).not.toBeInTheDocument()
})
it('reloads a fresh provider revision before retrying a save', async () => {
  setup()
  await screen.findByLabelText('MiMo API Key')
  const writes: Record<string, unknown>[] = []
  vi.mocked(fetch).mockImplementation(async (_path, options) => {
    if (options?.method === 'PUT') { writes.push(JSON.parse(options.body as string)); return Response.json({ ...settings, revision: 'r10' }) }
    return Response.json({ ...settings, revision: 'r9', key_configured: true })
  })
  fireEvent.click(screen.getByRole('button', { name: '重新读取配置' }))
  await screen.findByText('云端密钥：已配置')
  fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))
  await waitFor(() => expect(writes[0]?.expected_revision).toBe('r9'))
})
it('uses fresh personality metadata after its own save and an external update', async () => {
  setup()
  const input = await screen.findByLabelText('人格 Markdown 文件')
  const file = new File(['# one'], 'one.md')
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new TextEncoder().encode('# one').buffer })
  fireEvent.change(input, { target: { files: [file] } })
  await uploadSelectedFile()
  await screen.findByText('人格文件已保存。')
  await waitFor(() => expect(screen.getByRole('button', { name: '重新读取人格状态' })).toBeEnabled())
  const writes: Record<string, unknown>[] = []
  vi.mocked(fetch).mockImplementation(async (_path, options) => {
    if (options?.method === 'PUT') { writes.push(JSON.parse(options.body as string)); return Response.json({ exists: true, size_bytes: 5, revision: 'p10', updated_at: '' }) }
    return Response.json({ exists: true, size_bytes: 99, revision: 'p9', updated_at: '' })
  })
  fireEvent.click(screen.getByRole('button', { name: '重新读取人格状态' }))
  await screen.findByText(/99 字节/)
  fireEvent.change(input, { target: { files: [file] } })
  await uploadSelectedFile()
  await waitFor(() => expect(writes[0]?.expected_revision).toBe('p9'))
})
it('removes the saved key explicitly without sending a replacement secret', async () => {
  const writes = setup()
  fireEvent.change(await screen.findByLabelText('MiMo API Key'), { target: { value: 'discard-me' } })
  fireEvent.click(screen.getByLabelText('移除已保存的密钥'))
  fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body.remove_key).toBe(true)
  expect(writes[0].body).not.toHaveProperty('api_key')
  expect(screen.getByLabelText('MiMo API Key')).toHaveValue('')
})
it('rejects invalid UTF-8 without uploading or replacing the server file', async () => {
  const writes = setup()
  const file = new File([new Uint8Array([0xff])], 'broken.md')
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new Uint8Array([0xff]).buffer })
  fireEvent.change(await screen.findByLabelText('人格 Markdown 文件'), { target: { files: [file] } })
  await uploadSelectedFile()
  expect(await screen.findByText(/必须是有效的 UTF-8/)).toBeInTheDocument()
  expect(writes).toHaveLength(0)
})
it('waits for the server revision when a file is selected before personality metadata arrives', async () => {
  let release!: () => void
  const ready = new Promise<void>((resolve) => { release = resolve })
  const writes = setup(actor.capabilities, ready)
  const file = new File(['# pending'], 'pending.md')
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new TextEncoder().encode('# pending').buffer })
  fireEvent.change(await screen.findByLabelText('人格 Markdown 文件'), { target: { files: [file] } })
  expect(screen.getByRole('button', { name: '上传人格文件' })).toBeDisabled()
  expect(writes).toHaveLength(0)
  release()
  await uploadSelectedFile()
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body.expected_revision).toBe('p1')
})

it('saves the selected Token Plan region while retaining a previously stored key', async () => {
  const writes = setup()
  const endpoint = await screen.findByLabelText('MiMo 服务入口')
  expect(endpoint).toHaveValue('auto')
  fireEvent.change(endpoint, { target: { value: 'token_sgp' } })
  fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].body.mimo_endpoint).toBe('token_sgp')
  expect(writes[0].body).not.toHaveProperty('api_key')
  expect(screen.getByText(/token-plan-sgp.xiaomimimo.com/)).toBeInTheDocument()
})
