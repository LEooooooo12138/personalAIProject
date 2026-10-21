import { useState } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import type { AutomationDirectoryEntity } from './automationEntityDirectory'
import { PresenceEntityPicker } from './PresenceEntityPicker'

function entity(entity_id: string, extra: Partial<AutomationDirectoryEntity> = {}): AutomationDirectoryEntity {
  return { entity_id, name: '存在感应', domain: 'binary_sensor', device_class: 'occupancy', area_id: 'a', area_name: '书房', device_id: 'd_a', device_name: '书房传感器', device_kind: 'device', state: 'off', disabled: false, hidden: false, ...extra }
}
function Picker({ entities, initialValue = '', disabled = false }: { entities: AutomationDirectoryEntity[]; initialValue?: string; disabled?: boolean }) {
  const [value, setValue] = useState(initialValue)
  return <><PresenceEntityPicker entities={entities} value={value} onChange={setValue} disabled={disabled} /><output aria-label="选中实体">{value || '未选择'}</output></>
}
function select(label: string, value: string) { fireEvent.change(screen.getByRole('combobox', { name: label }), { target: { value } }) }

it('shows only devices and entities from the selected effective room', () => {
  render(<Picker entities={[entity('binary_sensor.study'), entity('binary_sensor.living', { area_id: 'b', area_name: '客厅', device_name: '跨房间传感器' })]} />)
  expect(screen.getByRole('combobox', { name: '设备' })).toBeDisabled()
  select('房间', 'b')
  expect(within(screen.getByRole('combobox', { name: '设备' })).getByRole('option', { name: '跨房间传感器' })).toBeInTheDocument()
  expect(within(screen.getByRole('combobox', { name: '设备' })).queryByRole('option', { name: '书房传感器' })).not.toBeInTheDocument()
  select('设备', 'd_a')
  const entities = screen.getByRole('combobox', { name: '实体' })
  expect(within(entities).getAllByRole('option')).toHaveLength(2)
  select('实体', 'binary_sensor.living')
  expect(screen.getByLabelText('选中实体')).toHaveTextContent('binary_sensor.living')
})

it('clears entity and downstream selections when room or device changes', () => {
  const entities = [entity('binary_sensor.one'), entity('binary_sensor.two', { device_id: 'd_b', device_name: '另一感应器' }), entity('binary_sensor.other', { area_id: 'b', area_name: '客厅' })]
  render(<Picker entities={entities} initialValue="binary_sensor.one" />)
  expect(screen.getByRole('combobox', { name: '房间' })).toHaveValue('a')
  select('设备', 'd_b')
  expect(screen.getByLabelText('选中实体')).toHaveTextContent('未选择')
  expect(screen.getByRole('combobox', { name: '实体' })).toHaveValue('')
  select('实体', 'binary_sensor.two')
  select('房间', 'b')
  expect(screen.getByLabelText('选中实体')).toHaveTextContent('未选择')
  expect(screen.getByRole('combobox', { name: '设备' })).toHaveValue('')
  expect(screen.getByRole('combobox', { name: '实体' })).toBeDisabled()
})

it('keeps same-name options distinguishable and sorts entities by their names', () => {
  render(<Picker entities={[entity('binary_sensor.z', { name: '前门感应器' }), entity('binary_sensor.two'), entity('binary_sensor.a', { name: '后门感应器' }), entity('binary_sensor.one')]} initialValue="binary_sensor.one" />)
  const labels = within(screen.getByRole('combobox', { name: '实体' })).getAllByRole('option').map((option) => option.textContent)
  expect(labels).toEqual(['请选择实体', '存在感应（同名项 1）', '存在感应（同名项 2）', '后门感应器', '前门感应器'])
  select('实体', 'binary_sensor.two')
  expect(screen.getByLabelText('选中实体')).toHaveTextContent('binary_sensor.two')
})

it('retains standalone entities in the unassigned room and uses Chinese unnamed fallbacks', () => {
  render(<Picker entities={[entity('input_boolean.solo', { domain: 'input_boolean', name: 'input_boolean.solo', area_id: 'u_other', area_name: '其他', device_id: 'e_solo', device_name: 'input_boolean.solo', device_kind: 'entity' }), entity('group.empty', { domain: 'group', name: '', device_id: 'd_empty', device_name: '' })]} />)
  select('房间', 'u_other')
  expect(within(screen.getByRole('combobox', { name: '设备' })).getByRole('option', { name: /独立实体.*未命名实体/ })).toBeInTheDocument()
  select('设备', 'e_solo')
  expect(within(screen.getByRole('combobox', { name: '实体' })).getByRole('option', { name: '未命名实体' })).toBeInTheDocument()
  select('房间', 'a')
  expect(within(screen.getByRole('combobox', { name: '设备' })).getByRole('option', { name: /未命名设备/ })).toBeInTheDocument()
})

it('keeps technical identifiers out of choices and pairs duplicate labels with identifiers in collapsed details', () => {
  render(<Picker entities={[entity('binary_sensor.one'), entity('binary_sensor.two')]} initialValue="binary_sensor.one" />)
  for (const label of ['房间', '设备', '实体']) {
    for (const option of within(screen.getByRole('combobox', { name: label })).getAllByRole('option')) {
      expect(option.textContent).not.toMatch(/binary_sensor\.|d_a/)
    }
  }
  const details = screen.getByText('标识对照').closest('details')!
  expect(details.open).toBe(false)
  expect(within(details).getByText('实体 · 存在感应（同名项 1）')).toBeInTheDocument()
  expect(within(details).getByText('binary_sensor.one')).toBeInTheDocument()
  expect(within(details).getByText('实体 · 存在感应（同名项 2）')).toBeInTheDocument()
  expect(within(details).getByText('binary_sensor.two')).toBeInTheDocument()
})

it('omits identifier comparison when every displayed name is distinct and named', () => {
  render(<Picker entities={[entity('binary_sensor.one')]} initialValue="binary_sensor.one" />)
  expect(screen.queryByText('标识对照')).not.toBeInTheDocument()
})

it('includes single backend unnamed fallbacks in collapsed identifier comparison', () => {
  render(<Picker entities={[entity('binary_sensor.unnamed', { name: '未命名实体', device_name: '未命名设备' })]} initialValue="binary_sensor.unnamed" />)
  expect(within(screen.getByRole('combobox', { name: '设备' })).getByRole('option', { name: '未命名设备' })).toBeInTheDocument()
  expect(within(screen.getByRole('combobox', { name: '实体' })).getByRole('option', { name: '未命名实体' })).toBeInTheDocument()
  const details = screen.getByText('标识对照').closest('details')!
  expect(details.open).toBe(false)
  expect(within(details).getByText('设备 · 未命名设备')).toBeInTheDocument()
  expect(within(details).getByText('d_a')).toBeInTheDocument()
  expect(within(details).getByText('实体 · 未命名实体')).toBeInTheDocument()
  expect(within(details).getByText('binary_sensor.unnamed')).toBeInTheDocument()
})

it('excludes unsupported, disabled, hidden and unavailable entities while retaining all supported domains', () => {
  render(<Picker entities={[
    entity('binary_sensor.valid'), entity('group.valid', { domain: 'group', state: 'home' }), entity('input_boolean.valid', { domain: 'input_boolean' }),
    entity('sensor.invalid', { domain: 'sensor' }), entity('light.invalid', { domain: 'light' }), entity('binary_sensor.hidden', { hidden: true }), entity('binary_sensor.disabled', { disabled: true }),
    entity('binary_sensor.unknown', { state: 'unknown' }), entity('binary_sensor.unavailable', { state: 'unavailable' }), entity('binary_sensor.null', { state: null }), entity('binary_sensor.empty', { state: '' }),
  ]} initialValue="group.valid" />)
  const values = within(screen.getByRole('combobox', { name: '实体' })).getAllByRole('option').map((option) => (option as HTMLOptionElement).value)
  expect(values).toEqual(['', 'binary_sensor.valid', 'group.valid', 'input_boolean.valid'])
})

it('announces an empty eligible directory and disables its choices', () => {
  render(<Picker entities={[entity('binary_sensor.hidden', { hidden: true })]} />)
  expect(screen.getByText(/没有可绑定的存在实体/)).toBeInTheDocument()
  for (const label of ['房间', '设备', '实体']) expect(screen.getByRole('combobox', { name: label })).toBeDisabled()
})

it('supports keyboard focus with native labelled selects and respects disabled editing', async () => {
  const user = userEvent.setup()
  const entities = [entity('binary_sensor.one')]
  const view = render(<Picker entities={entities} initialValue="binary_sensor.one" />)
  await user.tab(); expect(screen.getByRole('combobox', { name: '房间' })).toHaveFocus()
  await user.tab(); expect(screen.getByRole('combobox', { name: '设备' })).toHaveFocus()
  await user.tab(); expect(screen.getByRole('combobox', { name: '实体' })).toHaveFocus()
  view.rerender(<Picker entities={entities} initialValue="binary_sensor.one" disabled />)
  for (const label of ['房间', '设备', '实体']) expect(screen.getByRole('combobox', { name: label })).toBeDisabled()
})

it('follows external controlled selections and clears values removed by refreshed metadata', () => {
  const entities = [entity('binary_sensor.one'), entity('binary_sensor.two', { area_id: 'b', area_name: '客厅', device_id: 'd_b' })]
  const changes: string[] = []
  const view = render(<PresenceEntityPicker entities={entities} value="binary_sensor.one" onChange={(value) => changes.push(value)} />)
  view.rerender(<PresenceEntityPicker entities={entities} value="binary_sensor.two" onChange={(value) => changes.push(value)} />)
  expect(screen.getByRole('combobox', { name: '房间' })).toHaveValue('b')
  expect(screen.getByRole('combobox', { name: '设备' })).toHaveValue('d_b')
  view.rerender(<PresenceEntityPicker entities={[entities[0]]} value="binary_sensor.two" onChange={(value) => changes.push(value)} />)
  expect(screen.getByRole('combobox', { name: '实体' })).toHaveValue('')
  expect(changes).toEqual([''])
})
