import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AuthProvider, useAuth } from './auth'

const owner = { user: { id: 'a1', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }, capabilities: ['members:manage', 'sessions:read'], csrf_token: 'csrf-old' }
const newer = { user: { id: 'm2', username: 'new', display_name: '新用户', role: 'member', must_change_password: false }, capabilities: ['sessions:read'], csrf_token: 'csrf-new' }

function Probe() {
  const auth = useAuth()
  return <div><output>{auth.identity?.user.username ?? auth.status}</output><p role="alert">{auth.error}</p><button onClick={() => void auth.retryLogout()}>重试退出</button><button onClick={() => void auth.login('new', 'new-password-123')}>切换账号</button><button onClick={() => void auth.logout()}>退出</button><button onClick={() => void auth.changePassword('old-password-123', 'new-password-123')}>改密</button></div>
}
function show() { return render(<AuthProvider><Probe/></AuthProvider>) }
afterEach(() => vi.unstubAllGlobals())

it('discards a late me response after another account logs in', async () => {
  let releaseMe!: (response: Response) => void
  const me = new Promise<Response>((resolve) => { releaseMe = resolve })
  vi.stubGlobal('fetch', vi.fn(async (url: string) => url.endsWith('/auth/me') ? me : Response.json(newer)))
  show()
  fireEvent.click(screen.getByRole('button', { name: '切换账号' }))
  expect(await screen.findByText('new')).toBeInTheDocument()
  releaseMe(Response.json(owner))
  await waitFor(() => expect(screen.getByText('new')).toBeInTheDocument())
})

it('waits for old logout cookie removal before starting a new login', async () => {
  let releaseLogout!: (response: Response) => void
  const logout = new Promise<Response>((resolve) => { releaseLogout = resolve })
  const fetcher = vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return Response.json(owner)
    if (url.endsWith('/auth/logout')) return logout
    if (url.endsWith('/auth/login')) return Response.json(newer)
    throw new Error(url)
  })
  vi.stubGlobal('fetch', fetcher)
  show()
  expect(await screen.findByText('owner')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '退出' }))
  fireEvent.click(screen.getByRole('button', { name: '切换账号' }))
  expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/auth/login'))).toHaveLength(0)
  releaseLogout(new Response(null, { status: 204 }))
  expect(await screen.findByText('new')).toBeInTheDocument()
})

it('waits for every outstanding cookie-clearing response before starting a new login', async () => {
  let releasePassword!: (response: Response) => void
  let releaseLogout!: (response: Response) => void
  const password = new Promise<Response>((resolve) => { releasePassword = resolve })
  const logout = new Promise<Response>((resolve) => { releaseLogout = resolve })
  const fetcher = vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return Response.json(owner)
    if (url.endsWith('/auth/password')) return password
    if (url.endsWith('/auth/logout')) return logout
    if (url.endsWith('/auth/login')) return Response.json(newer)
    throw new Error(url)
  })
  vi.stubGlobal('fetch', fetcher)
  show()
  expect(await screen.findByText('owner')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '改密' }))
  fireEvent.click(screen.getByRole('button', { name: '退出' }))
  fireEvent.click(screen.getByRole('button', { name: '切换账号' }))
  releaseLogout(new Response(null, { status: 204 }))
  await new Promise((resolve) => setTimeout(resolve, 0))
  expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/auth/login'))).toHaveLength(0)
  releasePassword(new Response(null, { status: 204 }))
  expect(await screen.findByText('new')).toBeInTheDocument()
})

it('discards a late login result after sign-out', async () => {
  let releaseLogin!: (response: Response) => void
  const login = new Promise<Response>((resolve) => { releaseLogin = resolve })
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return Response.json({ error: { code: 'unauthenticated', request_id: 'r' } }, { status: 401 })
    if (url.endsWith('/auth/status')) return Response.json({ initialized: true })
    if (url.endsWith('/auth/login')) return login
    throw new Error(url)
  }))
  show()
  expect(await screen.findByText('anonymous')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '切换账号' }))
  fireEvent.click(screen.getByRole('button', { name: '退出' }))
  releaseLogin(Response.json(newer))
  await waitFor(() => expect(screen.getByText('anonymous')).toBeInTheDocument())
})



it('finishes failed logout when retry confirms the old session is already unauthenticated', async () => {
  let revoked = false
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return revoked
      ? Response.json({ error: { code: 'unauthenticated' } }, { status: 401 })
      : Response.json(owner)
    if (url.endsWith('/auth/logout')) return Response.json({ error: { code: 'unavailable' } }, { status: 503 })
    throw new Error(url)
  }))
  show()
  expect(await screen.findByText('owner')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /^退出$/ }))
  expect(await screen.findByText('logout_failed')).toBeInTheDocument()
  expect(screen.getByRole('alert')).toHaveTextContent('退出登录未完成')
  revoked = true
  fireEvent.click(screen.getByRole('button', { name: '重试退出' }))
  expect(await screen.findByText('anonymous')).toBeInTheDocument()
  expect(screen.getByRole('alert')).toBeEmptyDOMElement()
})
