import type { EntityView, SuggestionRule, SuggestionView } from './api'
import { formatEntityState } from './entityState'
export type AutomationEntity = Pick<EntityView, 'entity_id' | 'name' | 'domain' | 'device_class'> & { area_name?: string; device_name?: string }
const validEntity = (value: unknown): value is string => typeof value === 'string' && /^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$/.test(value)
export function automationEntityLabel(entity?: AutomationEntity): string {
  if (!entity) return '名称待核对'
  const readable = (name?: string) => name?.trim() && !validEntity(name.trim()) ? name.trim() : ''
  const name = readable(entity.name) || '未命名实体'
  return [...new Set([readable(entity.area_name), readable(entity.device_name), name].filter(Boolean))].join(' · ')
}
export function automationTitle(item: SuggestionView): string {
  const entities = new Map((item.entities ?? []).map(entity => [entity.entity_id, entity]))
  // Older responses may still put identifiers in the title. Never use them as display names.
  return item.title.replace(/[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*/g, id => item.entity_ids.includes(id) ? automationEntityLabel(entities.get(id)) : id)
}
const absent = (value: unknown) => value === undefined || value === ''
const validTime = (value: unknown) => typeof value === 'string' && /^(?:[0-9]|[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d(?:[.,]\d+)?)?$/.test(value)
const onlyKeys = (value: object, allowed: string[]) => Object.keys(value).every(key => allowed.includes(key))
export function summarizeRule(rule: SuggestionRule | null, entities: ReadonlyMap<string, AutomationEntity>): { supported: boolean; triggers: string[]; conditions: string[]; actions: string[]; unresolvedEntityIds: string[] } {
  const unresolved = new Set<string>()
  function label(id: string) { const entity = entities.get(id); if (!entity || !entity.name.trim() || validEntity(entity.name.trim()) || entity.name === '名称待核对') unresolved.add(id); return automationEntityLabel(entity) }
  function state(id: string, value: string) { const entity = entities.get(id); return formatEntityState({ state: value, domain: entity?.domain ?? (validEntity(id) ? id.split('.')[0] : undefined), device_class: entity?.device_class }) }
  const unsupported = () => ({ supported: false, triggers: [], conditions: [], actions: [], unresolvedEntityIds: [...unresolved] })
  if (!rule || !onlyKeys(rule, ['triggers', 'conditions', 'actions']) || !Array.isArray(rule.triggers) || !Array.isArray(rule.conditions) || !Array.isArray(rule.actions) || !rule.triggers.length || !rule.actions.length) return unsupported()
  const triggers: string[] = [], conditions: string[] = [], actions: string[] = []
  for (const trigger of rule.triggers) {
    if (!trigger || typeof trigger !== 'object' || !onlyKeys(trigger, ['platform','at','entity_id','from','to'])) return unsupported()
    if (trigger.platform === 'time' && validTime(trigger.at) && absent(trigger.entity_id) && absent(trigger.from) && absent(trigger.to)) triggers.push('每天 ' + trigger.at)
    else if (trigger.platform === 'state' && validEntity(trigger.entity_id) && typeof trigger.to === 'string' && trigger.to !== '' && absent(trigger.at) && (trigger.from === undefined || typeof trigger.from === 'string')) triggers.push(label(trigger.entity_id) + '：' + (trigger.from ? state(trigger.entity_id, trigger.from) + ' → ' : '变为 ') + state(trigger.entity_id, trigger.to))
    else return unsupported()
  }
  for (const condition of rule.conditions) {
    if (!condition || typeof condition !== 'object' || !onlyKeys(condition, ['condition','after','entity_id','state'])) return unsupported()
    if (condition.condition === 'sun' && condition.after === 'sunset' && absent(condition.entity_id) && absent(condition.state)) conditions.push('日落后')
    else if (condition.condition === 'state' && validEntity(condition.entity_id) && typeof condition.state === 'string' && condition.state !== '' && absent(condition.after)) conditions.push(label(condition.entity_id) + ' 为 ' + state(condition.entity_id, condition.state))
    else return unsupported()
  }
  for (const action of rule.actions) {
    if (!action || !onlyKeys(action, ['service','target']) || typeof action.service !== 'string' || !action.target || typeof action.target !== 'object' || !onlyKeys(action.target, ['entity_id']) || !validEntity(action.target.entity_id)) return unsupported()
    const parts = /^(light|switch|fan)\.(turn_on|turn_off)$/.exec(action.service)
    if (!parts || !action.target.entity_id.startsWith(parts[1] + '.')) return unsupported()
    actions.push((parts[2] === 'turn_on' ? '开启 ' : '关闭 ') + label(action.target.entity_id))
  }
  return { supported: true, triggers, conditions, actions, unresolvedEntityIds: [...unresolved] }
}
