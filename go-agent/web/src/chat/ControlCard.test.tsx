import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ControlCard from './ControlCard'
import type { ControlProposal } from './control-types'

const proposal: ControlProposal = { id: 'p-one', session_id: 'one', request_id: 'r-one', entity_id: 'light.one', name: '客厅主灯', area_name: '客厅', action: 'turn_on', status: 'pending', created_at: '2026-10-09T00:00:00Z', expires_at: '2099-10-09T00:02:00Z', before: { entity_id: 'light.one', name: '客厅主灯', state: 'off', observed_at: '2026-10-09T00:00:00Z' } }
afterEach(() => vi.useRealTimers())
const callbacks = () => ({ onConfirm: vi.fn(async (_id: string) => {}), onCancel: vi.fn(async (_id: string) => {}), onReconcile: vi.fn(async (_id: string) => {}) })
describe('control confirmation card', () => {
  it('displays the device, room, explicit action, observation and validity as a keyboard accessible separate confirmation', async () => {
    const handlers = callbacks()
    render(<ControlCard proposal={proposal} canControl {...handlers}/>)
    expect(screen.getByRole('region', { name: '客厅主灯控制提议' })).toHaveTextContent('客厅')
    expect(screen.getByText('打开')).toBeVisible()
    expect(screen.getByText(/观察时间/)).toBeVisible()
    expect(screen.getByText(/有效期/)).toBeVisible()
    const user = userEvent.setup()
    await user.tab()
    expect(screen.getByRole('button', { name: '确认打开客厅主灯' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(handlers.onConfirm).toHaveBeenCalledExactlyOnceWith('p-one')
  })
  it('locks both controls immediately during a double click and never claims an executing request was withdrawn', async () => {
    let finish: () => void = () => {}
    const handlers = callbacks()
    handlers.onConfirm.mockImplementation(() => new Promise<void>((resolve) => { finish = resolve }))
    const view = render(<ControlCard proposal={proposal} canControl {...handlers}/>)
    const confirm = screen.getByRole('button', { name: '确认打开客厅主灯' })
    fireEvent.click(confirm); fireEvent.click(confirm)
    expect(handlers.onConfirm).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: '取消提议' })).toBeDisabled()
    view.rerender(<ControlCard proposal={{ ...proposal, status: 'executing' }} canControl {...handlers}/>)
    expect(screen.queryByRole('button', { name: '取消提议' })).toBeNull()
    expect(screen.queryByText(/已取消|已撤回/)).toBeNull()
    await act(async () => finish())
  })
  it('hides member controls while leaving the observation readable', () => {
    render(<ControlCard proposal={proposal} canControl={false} {...callbacks()}/>)
    expect(screen.queryByRole('button', { name: /确认|取消/ })).toBeNull()
    expect(screen.getByText(/当前状态/)).toHaveTextContent('关闭')
  })
  it('disables confirmation when the live expiry deadline passes', async () => {
    vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-09T00:01:59Z'))
    const handlers = callbacks()
    render(<ControlCard proposal={{ ...proposal, expires_at: '2026-10-09T00:02:00Z' }} canControl {...handlers}/>)
    await act(async () => vi.advanceTimersByTime(1100))
    expect(screen.getByRole('button', { name: '确认打开客厅主灯' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '确认打开客厅主灯' }))
    expect(handlers.onConfirm).not.toHaveBeenCalled()
  })
  it.each(['expired', 'cancelled', 'failed', 'succeeded'] as const)('offers no execution controls for %s', (status) => {
    render(<ControlCard proposal={{ ...proposal, status }} canControl {...callbacks()}/>)
    expect(screen.queryByRole('button')).toBeNull()
  })
  it('offers only read-only reconciliation for unknown, with the physical verification caveat', () => {
    const handlers = callbacks()
    render(<ControlCard proposal={{ ...proposal, status: 'unknown', error_code: 'secret upstream error', after: { ...proposal.before!, state: 'on' } }} canControl {...handlers}/>)
    expect(screen.getByText(/结果尚未确认/)).toBeVisible()
    expect(screen.getByText(/现场/)).toBeVisible()
    expect(screen.queryByText(/secret upstream/)).toBeNull()
    expect(screen.getAllByRole('button')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: '核对状态' }))
    expect(handlers.onReconcile).toHaveBeenCalledExactlyOnceWith('p-one')
  })
})

it('generates distinct cryptographic UUIDs when randomUUID is absent on local HTTP', async () => {
  const { newChatRequestId } = await import('./control-types')
  const original = globalThis.crypto
  vi.stubGlobal('crypto', { getRandomValues: original.getRandomValues.bind(original) })
  try {
    const first = newChatRequestId(); const second = newChatRequestId()
    expect(first).toMatch(/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/)
    expect(second).not.toBe(first)
  } finally { vi.stubGlobal('crypto', original) }
})
