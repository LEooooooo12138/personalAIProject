import { useEffect, useId, useState } from 'react'
import type { AutomationDirectoryEntity } from './automationEntityDirectory'
import { Disclosure, EmptyState, Field, KeyValueList, Select, Stack } from './ui'

export interface PresenceEntityPickerProps {
  entities: AutomationDirectoryEntity[]
  value: string
  onChange: (entityId: string) => void
  disabled?: boolean
  purpose?: 'presence' | 'source' | 'target'
}

export function isPresenceCandidate(entity: AutomationDirectoryEntity): boolean {
  return ['group', 'binary_sensor', 'input_boolean'].includes(entity.domain)
    && !entity.disabled && !entity.hidden
    && entity.state != null && entity.state.trim() !== ''
    && !['unknown', 'unavailable'].includes(entity.state)
}

function entityLabel(entity: AutomationDirectoryEntity): string {
  const name = entity.name.trim()
  return name && name !== entity.entity_id ? name : '未命名实体'
}

function deviceLabel(entity: AutomationDirectoryEntity): string {
  if (entity.device_kind === 'entity') return `独立实体 · ${entityLabel(entity)}`
  const name = entity.device_name.trim()
  return name && name !== entity.device_id ? name : '未命名设备'
}

function compareLabels(a: { id: string; label: string }, b: { id: string; label: string }) {
  return a.label.localeCompare(b.label, 'zh-CN') || a.id.localeCompare(b.id)
}

function distinguishLabels(items: { id: string; label: string; unnamed: boolean }[]) {
  return items.map((item, index) => {
    const duplicate = items.filter((other) => other.label === item.label).length > 1
    const ordinal = items.slice(0, index + 1).filter((other) => other.label === item.label).length
    return { ...item, needsIdentifier: duplicate || item.unnamed, label: duplicate ? `${item.label}（同名项 ${ordinal}）` : item.label }
  })
}

export function isActionCandidate(entity: AutomationDirectoryEntity): boolean {
  return ['light', 'switch', 'fan'].includes(entity.domain) && availableCandidate(entity)
}
export function isSourceCandidate(entity: AutomationDirectoryEntity): boolean {
  return ['light', 'switch', 'fan', 'binary_sensor', 'input_boolean'].includes(entity.domain) && availableCandidate(entity) && ['on', 'off'].includes(entity.state ?? '')
}
function availableCandidate(entity: AutomationDirectoryEntity): boolean {
  return !entity.disabled && !entity.hidden && entity.state != null && entity.state.trim() !== '' && !['unknown', 'unavailable'].includes(entity.state)
}

export function PresenceEntityPicker({ entities, value, onChange, disabled = false, purpose = 'presence' }: PresenceEntityPickerProps) {
  const id = useId()
  const candidates = entities.filter(purpose === 'target' ? isActionCandidate : purpose === 'source' ? isSourceCandidate : isPresenceCandidate)
  const selected = candidates.find((entity) => entity.entity_id === value)
  const [areaDraft, setAreaDraft] = useState(selected?.area_id ?? '')
  const [deviceDraft, setDeviceDraft] = useState(selected?.device_id ?? '')
  const areas = distinguishLabels([...new Map(candidates.map((entity) => [entity.area_id, { id: entity.area_id, label: entity.area_name.trim() || '未分配房间', unnamed: !entity.area_name.trim() }])).values()].sort(compareLabels))
  const desiredArea = selected?.area_id ?? areaDraft
  const areaId = areas.some((area) => area.id === desiredArea) ? desiredArea : ''
  const roomEntities = candidates.filter((entity) => entity.area_id === areaId)
  const devices = distinguishLabels([...new Map(roomEntities.map((entity) => [entity.device_id, { id: entity.device_id, label: deviceLabel(entity), unnamed: entity.device_kind === 'entity' ? entityLabel(entity) === '未命名实体' : !entity.device_name.trim() || entity.device_name.trim() === entity.device_id || entity.device_name.trim() === '未命名设备' }])).values()].sort(compareLabels))
  const desiredDevice = selected?.device_id ?? deviceDraft
  const deviceId = devices.some((device) => device.id === desiredDevice) ? desiredDevice : ''
  const options = distinguishLabels(roomEntities.filter((entity) => entity.device_id === deviceId).map((entity) => ({ id: entity.entity_id, label: entityLabel(entity), unnamed: !entity.name.trim() || entity.name.trim() === entity.entity_id || entity.name.trim() === '未命名实体' })).sort(compareLabels))
  const identifiers = [
    ...areas.filter((area) => area.needsIdentifier).map((area) => ({ id: `area:${area.id}`, label: `房间 · ${area.label}`, value: area.id })),
    ...devices.filter((device) => device.needsIdentifier).map((device) => ({ id: `device:${device.id}`, label: `设备 · ${device.label}`, value: device.id })),
    ...options.filter((entity) => entity.needsIdentifier).map((entity) => ({ id: `entity:${entity.id}`, label: `实体 · ${entity.label}`, value: entity.id })),
  ]

  useEffect(() => {
    if (selected) { setAreaDraft(selected.area_id); setDeviceDraft(selected.device_id) }
  }, [selected?.area_id, selected?.device_id])
  useEffect(() => { if (value && !selected) onChange('') }, [value, selected, onChange])

  return <Stack gap={12}>
    {candidates.length === 0 && <EmptyState title={purpose === 'presence' ? '没有可绑定的存在实体' : '没有受支持的可用实体'} description="请检查实体的房间归属及当前可用状态。" />}
    <Field id={`${id}-area`} label="房间">
      <Select id={`${id}-area`} value={areaId} disabled={disabled || areas.length === 0} onChange={(event) => {
        setAreaDraft(event.target.value); setDeviceDraft(''); onChange('')
      }}>
        <option value="">请选择房间</option>
        {areas.map((area) => <option key={area.id} value={area.id}>{area.label}</option>)}
      </Select>
    </Field>
    <Field id={`${id}-device`} label="设备">
      <Select id={`${id}-device`} value={deviceId} disabled={disabled || !areaId || devices.length === 0} onChange={(event) => {
        setDeviceDraft(event.target.value); onChange('')
      }}>
        <option value="">请选择设备或独立实体</option>
        {devices.map((device) => <option key={device.id} value={device.id}>{device.label}</option>)}
      </Select>
    </Field>
    <Field id={`${id}-entity`} label="实体">
      <Select id={`${id}-entity`} value={selected && selected.area_id === areaId && selected.device_id === deviceId ? value : ''} disabled={disabled || !deviceId || options.length === 0} onChange={(event) => onChange(event.target.value)}>
        <option value="">请选择实体</option>
        {options.map((entity) => <option key={entity.id} value={entity.id}>{entity.label}</option>)}
      </Select>
    </Field>
    {identifiers.length > 0 && <Disclosure title="标识对照"><KeyValueList items={identifiers} /></Disclosure>}
  </Stack>
}
