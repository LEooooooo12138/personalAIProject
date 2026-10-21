import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ControlCard, { DeviceResultCard } from './ControlCard'
import { entityStateLabel } from '../familyViews'
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


it('offers GET refresh while executing, including a bounded-watch stale result', () => {
 const refresh = vi.fn(async () => {})
 render(<ControlCard proposal={{ ...proposal, status: 'executing' }} canControl {...callbacks()} onRefresh={refresh} refreshError="暂未取得最新结果"/>)
 fireEvent.click(screen.getByRole('button', { name: '刷新状态' }))
 expect(refresh).toHaveBeenCalledExactlyOnceWith('p-one')
 expect(screen.getByRole('alert')).toHaveTextContent('暂未取得最新结果')
 expect(screen.queryByRole('button', { name: /确认|取消/ })).toBeNull()
})

it.each([['door', '打开'], ['tamper', '检测到防拆'], ['unverified', 'on']])('uses verified binary_sensor %s semantics consistently with the area view', (device_class, label) => {
 const result = { entity_id: 'binary_sensor.front', name: '传感器', state: 'on', observed_at: '2026-10-09', domain: 'binary_sensor', device_class }
 render(<DeviceResultCard result={result}/>)
 expect(screen.getByRole('region', { name: '传感器设备状态' })).toHaveTextContent('当前状态：' + label)
 expect(entityStateLabel({ ...result, unit: null, last_changed: null, last_updated: null, disabled: false, hidden: false, category: null })).toBe(label)
})
it('preserves legacy sensor on as raw state and displays a numeric unit independently', () => {
 const view = render(<DeviceResultCard result={{ entity_id: 'sensor.old', name: '旧传感器', state: 'on', observed_at: '' }}/>)
 expect(screen.getByRole('region')).toHaveTextContent('当前状态：on')
 view.rerender(<DeviceResultCard result={{ entity_id: 'sensor.temp', domain: 'sensor', name: '温度', state: '24', unit: '°C', observed_at: '' }}/>)
 expect(screen.getByRole('region')).toHaveTextContent('24 °C')
})

it('never re-enables a proposal once expiry was observed even if the wall clock rolls back', async () => {
 vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-09T00:01:59Z'))
 const handlers = callbacks()
 render(<ControlCard proposal={{ ...proposal, expires_at: '2026-10-09T00:02:00Z' }} canControl {...handlers}/>)
 await act(async () => vi.advanceTimersByTime(1500))
 expect(screen.getByRole('button', { name: '确认打开客厅主灯' })).toBeDisabled()
 vi.setSystemTime(new Date('2026-10-09T00:01:00Z'))
 await act(async () => vi.advanceTimersByTime(500))
 expect(screen.getByRole('button', { name: '确认打开客厅主灯' })).toBeDisabled()
 fireEvent.click(screen.getByRole('button', { name: '确认打开客厅主灯' }))
 expect(handlers.onConfirm).not.toHaveBeenCalled()
})
