import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider, useAuth } from '../auth'
import MembersPage from './MembersPage'

const user = { id: 'a1', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }
const members = ['甲', '乙'].map((name, index) => ({ id: 'm' + index, username: 'member' + index, display_name: name, role: 'member', disabled: false, must_change_password: false }))
afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () { this.open = true }
  HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new Event('close')) }
})
function Ready() { const auth = useAuth(); return auth.identity ? <MembersPage/> : null }
function show() { return render(<MemoryRouter><AuthProvider><Ready/></AuthProvider></MemoryRouter>) }
function setup() {
  let complete!: (response: Response) => void
  const response = new Promise<Response>((resolve) => { complete = resolve })
  const fetcher = vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith('/auth/me')) return Response.json({ user, capabilities: ['members:manage'], csrf_token: 'token' })
    if (url.endsWith('/admin/members') && (!options?.method || options.method === 'GET')) return Response.json({ members })
    return response
  })
  vi.stubGlobal('fetch', fetcher)
  return { fetcher, complete }
}

it('pending_reset_cannot_replace_another_member_dialog', async () => {
  const transport = setup(); show()
  fireEvent.click(await screen.findByRole('button', { name: '重置口令：甲' }))
  fireEvent.click(screen.getByRole('button', { name: '确认重置' }))
  const dialog = screen.getByRole('dialog')
  fireEvent(dialog, new Event('cancel', { bubbles: true, cancelable: true }))
  expect(dialog).toBeInTheDocument()
  expect(within(dialog).getByRole('button', { name: '取消' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '重置口令：乙' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: '重置口令：乙' }))
  fireEvent.click(within(dialog).getByRole('button', { name: '重置中…' }))
  await act(async () => transport.complete(Response.json({ temporary_password: 'one-use-secret' })))
  expect(await screen.findByText('one-use-secret')).toBeInTheDocument()
  expect(screen.getByRole('dialog')).toHaveTextContent('member0')
  expect(transport.fetcher.mock.calls.filter(([url]) => url.endsWith('/reset-password'))).toHaveLength(1)
})

it('pending_rename_cannot_close_new_dialog', async () => {
  const transport = setup(); show()
  fireEvent.click(await screen.findByRole('button', { name: '改名：甲' }))
  fireEvent.change(screen.getByLabelText('显示名称'), { target: { value: '甲的新名字' } })
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(screen.getByRole('button', { name: '改名：乙' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: '改名：乙' }))
  expect(screen.getByRole('dialog')).toHaveTextContent('甲')
  await act(async () => transport.complete(Response.json({ user: { ...members[0], display_name: '甲的新名字' } })))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(await screen.findByText('甲的新名字')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '改名：乙' }))
  expect(screen.getByLabelText('显示名称')).toHaveValue('乙')
})

it('unmounted_reset_does_not_expose_its_late_password_on_a_new_page', async () => {
  const transport = setup(); const first = show()
  fireEvent.click(await screen.findByRole('button', { name: '重置口令：甲' }))
  fireEvent.click(screen.getByRole('button', { name: '确认重置' }))
  first.unmount(); show()
  await screen.findByRole('button', { name: '重置口令：乙' })
  await act(async () => transport.complete(Response.json({ temporary_password: 'old-secret' })))
  expect(screen.queryByText('old-secret')).not.toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})
