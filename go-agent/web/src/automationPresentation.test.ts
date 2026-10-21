import { expect, it } from 'vitest'
import type { SuggestionRule } from './api'
import { summarizeRule } from './automationPresentation'
const base: SuggestionRule = { triggers: [{ platform: 'time', at: '18:30' }], conditions: [{ condition: 'sun', after: 'sunset' }], actions: [{ service: 'light.turn_on', target: { entity_id: 'light.living' } }] }
it('summarizes_supported_semantics_in_the_original_action_order', () => {
  const result = summarizeRule({ ...base, actions: [...base.actions, { service: 'fan.turn_off', target: { entity_id: 'fan.study' } }] }, new Map([['light.living', { entity_id: 'light.living', name: '客厅灯', domain: 'light' }]]))
  expect(result.supported).toBe(true)
  expect(result.triggers).toEqual(['每天 18:30'])
  expect(result.conditions).toEqual(['日落后'])
  expect(result.actions).toEqual(['开启 客厅灯', '关闭 名称待核对'])
  expect(result.unresolvedEntityIds).toEqual(['fan.study'])
})
it.each(['25:00', '18:99', '18', '18:30:90'])('rejects_invalid_time_%s', (at) => {
  expect(summarizeRule({ ...base, triggers: [{ platform: 'time', at }] }, new Map()).supported).toBe(false)
})
it('keeps_unknown_presence_state_as_raw_data_without_guessing_no_one_is_home', () => {
  const result = summarizeRule({ ...base, conditions: [{ condition: 'state', entity_id: 'binary_sensor.family', state: 'off' }] }, new Map())
  expect(result.conditions).toEqual(['名称待核对 为 off'])
  expect(result.conditions.join('')).not.toContain('无人')
})
it.each([
  { ...base, triggers: [] }, { ...base, actions: [] }, { ...base, triggers: [{ platform: 'event' }] },
  { ...base, conditions: [{ condition: 'sun', after: 'sunrise' }] },
  { ...base, actions: [{ service: 'switch.turn_on', target: { entity_id: 'light.living' } }] },
  { ...base, actions: [{ service: 'script.turn_on', target: { entity_id: 'script.test' } }] },
])('does_not_claim_support_for_unknown_or_domain_mismatched_rules', (rule) => {
  expect(summarizeRule(rule, new Map()).supported).toBe(false)
})
it('supports_state_triggers_and_conditions_with_verified_class_semantics', () => {
  const entities = new Map([['binary_sensor.door', { entity_id: 'binary_sensor.door', domain: 'binary_sensor', device_class: 'door', name: '门磁' }]])
  const result = summarizeRule({ ...base, triggers: [{ platform: 'state', entity_id: 'binary_sensor.door', from: 'off', to: 'on' }], conditions: [{ condition: 'state', entity_id: 'binary_sensor.door', state: 'off' }] }, entities)
  expect(result.triggers).toEqual(['门磁：关闭 → 打开'])
  expect(result.conditions).toEqual(['门磁 为 关闭'])
})
it.each(['7:30', '7:30:15'])('accepts server-valid one-digit hour %s', at => {
  expect(summarizeRule({ ...base, triggers: [{ platform: 'time', at }] }, new Map()).supported).toBe(true)
})
it.each([
  { ...base, weekday: ['mon'] },
  { ...base, triggers: [{ platform: 'time', at: '18:30', weekday: ['mon'] }] },
  { ...base, conditions: [{ condition: 'sun', after: 'sunset', before: 'sunrise' }] },
  { ...base, actions: [{ service: 'light.turn_on', target: { entity_id: 'light.living', area_id: 'unknown' } }] },
])('does not claim support for fields the Go validator rejects', rule => {
  expect(summarizeRule(rule, new Map()).supported).toBe(false)
})
