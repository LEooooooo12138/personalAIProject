import { expect, it } from 'vitest'
import type { IntegrationsStatus } from './api'
import { integrationStatusItems } from './homeStatus'
const status: IntegrationsStatus = { ha: { connection: 'connected', freshness: 'stale', observed_at: null, last_attempt_at: null, last_success_at: null, error_code: null }, ollama: { connection: 'connected', checked_at: null, models: [{ name: 'test-model', available: true }], error_code: null } }
it('shows_connected_and_stale_as_separate_facts', () => {
  const facts = integrationStatusItems(status, false)
  expect(facts.find((item) => item.id === 'ha-connection')?.tone).toBe('success')
  expect(facts.find((item) => item.id === 'ha-freshness')?.tone).toBe('warning')
})
it('installed_models_are_not_generation_health', () => {
  const model = integrationStatusItems(status, false).find((item) => item.id === 'ollama-models')!
  expect(model.tone).toBe('neutral')
  expect(model.detail).toContain('不代表实际生成已验证')
})
it('a_failed_read_does_not_turn_previous_data_into_current_success', () => {
  expect(integrationStatusItems(status, true).find((item) => item.id === 'ha-connection')?.tone).toBe('warning')
})
it('missing_data_is_unknown', () => {
  const facts = integrationStatusItems(null, false)
  expect(facts.some((item) => item.tone === 'success')).toBe(false)
  expect(facts.every((item) => !item.label.includes('0'))).toBe(true)
})
