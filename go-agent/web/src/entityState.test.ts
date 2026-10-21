import { describe, expect, it } from 'vitest'
import { formatEntityState } from './entityState'
import { deviceStateText, isDeviceResult } from './chat/control-types'
const legacy = { entity_id: 'sensor.temperature', name: '温度', state: 'on', observed_at: '' }
describe('verified entity state semantics', () => {
  it.each(['light', 'switch', 'fan', 'input_boolean'])('%s uses power state', domain => {
    expect(formatEntityState({ domain, state: 'on' })).toBe('开启')
    expect(formatEntityState({ domain, state: 'off' })).toBe('关闭')
  })
  it.each(['door', 'window', 'opening'])('%s uses opening state', device_class => {
    expect(formatEntityState({ domain: 'binary_sensor', device_class, state: 'on' })).toBe('打开')
    expect(formatEntityState({ domain: 'binary_sensor', device_class, state: 'off' })).toBe('关闭')
  })
  it.each(['motion', 'occupancy', 'presence'])('%s uses detected state', device_class => {
    expect(formatEntityState({ domain: 'binary_sensor', device_class, state: 'on' })).toBe('检测到')
    expect(formatEntityState({ domain: 'binary_sensor', device_class, state: 'off' })).toBe('未检测到')
  })
  it('preserves uncertainty and unverified values', () => {
    expect(formatEntityState({ state: null })).toBe('暂无状态')
    expect(formatEntityState({ state: 'unknown' })).toBe('状态未知')
    expect(formatEntityState({ state: 'unavailable' })).toBe('不可用')
    expect(formatEntityState({ state: 'on', domain: 'sensor' })).toBe('on')
    expect(formatEntityState({ state: 'off', domain: 'binary_sensor', device_class: 'unverified' })).toBe('off')
    expect(formatEntityState({ state: 'on', domain: 'binary_sensor', device_class: 'tamper' })).toBe('检测到防拆')
    expect(formatEntityState({ state: 'off', domain: 'binary_sensor', device_class: 'tamper' })).toBe('未检测到防拆')
  })
  it('only infers a domain from a valid legacy entity id', () => {
    expect(isDeviceResult(legacy)).toBe(true)
    expect(deviceStateText(legacy)).toBe('on')
    expect(deviceStateText({ ...legacy, entity_id: 'switch.channel_1' })).toBe('开启')
    for (const entity_id of ['switch.', 'switch.bad.value', '.channel', 'switch on']) expect(deviceStateText({ ...legacy, entity_id })).toBe('on')
  })
  it('validates all present optional metadata', () => {
    expect(isDeviceResult({ ...legacy, device_class: null, unit: null, domain: 'sensor' })).toBe(true)
    for (const metadata of [{ domain: null }, { domain: 2 }, { device_class: {} }, { unit: 4 }, { unit: undefined }]) expect(isDeviceResult({ ...legacy, ...metadata })).toBe(false)
  })
})
