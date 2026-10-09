import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from './App'

const admin = { id: 'a1', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }
const member = { ...admin, id: 'm1', username: 'mei', display_name: '梅', role: 'member' }
const identity = (user = admin, capabilities = ['chat:use', 'sessions:read', 'members:manage']) => ({ user, capabilities, csrf_token: 'csrf-1' })
const json = (value: unknown, status = 200) => Response.json(value, { status })

afterEach(() => vi.unstubAllGlobals())

function show(path = '/app/') { return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>) }

it('shows login failure safely and does not reveal the private home', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json({ error: { code: 'unauthenticated', message: 'Authentication required', request_id: 'r1' } }, 401)
    if (url.endsWith('/auth/status')) return json({ initialized: true })
    return json({ error: { code: 'unauthenticated', message: '<b>wrong</b>', request_id: 'r2' } }, 401)
  }))
  show()
  fireEvent.change(await screen.findByLabelText('用户名'), { target: { value: 'owner' } })
  fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'wrong' } })
  fireEvent.click(screen.getByRole('button', { name: '登录' }))
  expect(await screen.findByText(/用户名或密码不正确/)).toBeInTheDocument()
  expect(screen.queryByText(/最近对话/)).not.toBeInTheDocument()
  expect(screen.queryByText('<b>wrong</b>')).not.toBeInTheDocument()
})

it('blocks the home while a temporary password must be changed', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json(identity({ ...member, must_change_password: true }, []))
    throw new Error(`unexpected private request ${url}`)
  }))
  show('/app/')
  expect(await screen.findByRole('heading', { name: '设置新密码' })).toBeInTheDocument()
  expect(screen.queryByText(/最近对话/)).not.toBeInTheDocument()
})

it('shows the real uninitialized state returned by auth/me', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => json({ error: { code: 'not_initialized', message: 'Administrator initialization required', request_id: 'r3' } }, 503)))
  show()
  expect(await screen.findByText(/家庭控制台尚未初始化/)).toBeInTheDocument()
  expect(screen.getByLabelText('用户名')).toBeDisabled()
})

it('denies a member opening the management URL directly', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json(identity(member, ['chat:use', 'sessions:read']))
    throw new Error(`unexpected management request ${url}`)
  }))
  show('/app/members')
  expect(await screen.findByText('无权查看成员管理')).toBeInTheDocument()
  expect(screen.queryByText('创建成员')).not.toBeInTheDocument()
})

it('keeps saved history reachable without chat capability', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json(identity(member, ['sessions:read']))
    if (url.endsWith('/sessions')) return json({ sessions: [] })
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/chat')
  expect((await screen.findAllByRole('link', { name: '对话记录' })).length).toBeGreaterThan(0)
  expect(screen.getByText('当前仅可阅读历史记录。')).toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: '消息内容' })).not.toBeInTheDocument()
})

it('hides and denies the chat route without chat or history capability', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json(identity(member, []))
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/chat')
  expect(await screen.findByText('当前无法使用对话')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /对话/ })).not.toBeInTheDocument()
  expect(screen.queryByText('最近对话')).not.toBeInTheDocument()
})

it('drops a late private sessions response after sign-out', async () => {
  let release!: (value: Response) => void
  const delayed = new Promise<Response>((resolve) => { release = resolve })
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json(identity())
    if (url.endsWith('/sessions')) return delayed
    if (url.endsWith('/auth/logout')) return new Response(null, { status: 204 })
    throw new Error(`unexpected ${url}`)
  }))
  show()
  fireEvent.click((await screen.findAllByRole('button', { name: '退出登录' }))[0])
  await screen.findByRole('button', { name: '登录' })
  release(json({ sessions: [{ id: 'old', started_at: '2026-01-01T00:00:00Z', last_active_at: '2026-01-01T00:00:00Z', round_count: 1, message_count: 2, preview: 'private old conversation' }] }))
  await waitFor(() => expect(screen.queryByText('private old conversation')).not.toBeInTheDocument())
})

it('creates a member and clears the temporary password when the dialog closes', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
    if (url.endsWith('/auth/me')) return json(identity())
    if (url.endsWith('/admin/members') && options.method === 'POST') return json({ user: { ...member, disabled: false, must_change_password: true }, temporary_password: 'one-time-secret' }, 201)
    if (url.endsWith('/admin/members')) return json({ members: [] })
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/members')
  fireEvent.click(await screen.findByRole('button', { name: '创建成员' }))
  fireEvent.change(screen.getByLabelText('登录名'), { target: { value: 'mei' } })
  fireEvent.change(screen.getByLabelText('显示名称'), { target: { value: '梅' } })
  fireEvent.click(screen.getByRole('button', { name: '确认创建' }))
  expect(await screen.findByText('one-time-secret')).toBeInTheDocument()
  fireEvent.click(screen.getAllByRole('button', { name: '关闭' }).at(-1)!)
  expect(screen.queryByText('one-time-secret')).not.toBeInTheDocument()
})

it('clears an already loaded member list when a reset write returns 401', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
    if (url.endsWith('/auth/me')) return json(identity())
    if (url.endsWith('/admin/members') && options.method === 'GET') return json({ members: [{ ...member, disabled: false }] })
    if (url.endsWith('/reset-password')) return json({ error: { code: 'unauthenticated', message: 'expired', request_id: 'r4' } }, 401)
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/members')
  expect(await screen.findByText('@mei')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '重置口令：梅' }))
  fireEvent.click(screen.getByRole('button', { name: '确认重置' }))
  expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  expect(screen.queryByText('@mei')).not.toBeInTheDocument()
})

it('clears member management after a create write returns 401', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
    if (url.endsWith('/auth/me')) return json(identity())
    if (url.endsWith('/admin/members') && options.method === 'GET') return json({ members: [{ ...member, disabled: false }] })
    if (url.endsWith('/admin/members') && options.method === 'POST') return json({ error: { code: 'unauthenticated', message: 'expired', request_id: 'r5' } }, 401)
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/members')
  expect(await screen.findByText('@mei')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '创建成员' }))
  fireEvent.change(screen.getByLabelText('登录名'), { target: { value: 'new' } })
  fireEvent.change(screen.getByLabelText('显示名称'), { target: { value: '新成员' } })
  fireEvent.click(screen.getByRole('button', { name: '确认创建' }))
  expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  expect(screen.queryByText('@mei')).not.toBeInTheDocument()
})

it('ignores an old member write 401 after another account logs in', async () => {
  let releaseCreate!: (response: Response) => void
  const create = new Promise<Response>((resolve) => { releaseCreate = resolve })
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
    if (url.endsWith('/auth/me')) return json(identity())
    if (url.endsWith('/auth/logout')) return new Response(null, { status: 204 })
    if (url.endsWith('/auth/login')) return json(identity(member, []))
    if (url.endsWith('/admin/members') && options.method === 'GET') return json({ members: [] })
    if (url.endsWith('/admin/members') && options.method === 'POST') return create
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/members')
  fireEvent.click(await screen.findByRole('button', { name: '创建成员' }))
  fireEvent.change(screen.getByLabelText('登录名'), { target: { value: 'new' } })
  fireEvent.change(screen.getByLabelText('显示名称'), { target: { value: '新成员' } })
  fireEvent.click(screen.getByRole('button', { name: '确认创建' }))
  fireEvent.click(screen.getAllByRole('button', { name: '退出登录' })[0])
  fireEvent.change(await screen.findByLabelText('用户名'), { target: { value: 'mei' } })
  fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'new-password-123' } })
  fireEvent.click(screen.getByRole('button', { name: '登录' }))
  expect(await screen.findByText('你好，梅')).toBeInTheDocument()
  releaseCreate(json({ error: { code: 'unauthenticated', message: 'expired', request_id: 'r7' } }, 401))
  await waitFor(() => expect(screen.getByText('你好，梅')).toBeInTheDocument())
})

it('clears the private account page when password write returns 401', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json(identity())
    if (url.endsWith('/auth/password')) return json({ error: { code: 'unauthenticated', message: 'expired', request_id: 'r6' } }, 401)
    throw new Error(`unexpected ${url}`)
  }))
  show('/app/account')
  fireEvent.change(await screen.findByLabelText('当前密码'), { target: { value: 'old-password-123' } })
  fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'new-password-123' } })
  fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: 'new-password-123' } })
  fireEvent.click(screen.getByRole('button', { name: '保存新密码' }))
  expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  expect(screen.queryByText('个人资料')).not.toBeInTheDocument()
})
