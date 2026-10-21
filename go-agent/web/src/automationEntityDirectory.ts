import { ApiError, requestConsole, type AreaDevices, type AreaList } from './api'
import type { AutomationEntity } from './automationPresentation'
export interface AutomationDirectoryEntity extends AutomationEntity {
  area_id: string
  area_name: string
  device_id: string
  device_name: string
  device_kind: 'device' | 'entity'
  state: string | null
  disabled: boolean
  hidden: boolean
}

export async function readAutomationEntityDirectory(signal: AbortSignal): Promise<ReadonlyMap<string, AutomationDirectoryEntity>> {
  signal.throwIfAborted()
  const directory = await requestConsole<AreaList>('/areas', { signal })
  signal.throwIfAborted()
  if (directory.meta.freshness !== 'fresh' || directory.meta.connection !== 'connected') throw new ApiError(503, 'ha_unavailable')
  const entities = new Map<string, AutomationDirectoryEntity>()
  let next = 0
  const controller = new AbortController()
  const readSignal = AbortSignal.any([signal, controller.signal])
  async function worker() {
    while (next < directory.areas.length) {
      readSignal.throwIfAborted()
      const area = directory.areas[next++]
      const list = await requestConsole<AreaDevices>('/areas/' + encodeURIComponent(area.id) + '/devices', { signal: readSignal })
      readSignal.throwIfAborted()
      if (list.meta.freshness !== 'fresh' || list.meta.connection !== 'connected') throw new ApiError(503, 'ha_unavailable')
      for (const device of list.devices) for (const entity of device.entities) {
        if (!entity.disabled) entities.set(entity.entity_id, {
          entity_id: entity.entity_id,
          name: entity.name,
          domain: entity.domain,
          device_class: entity.device_class,
          area_id: list.area.id,
          area_name: list.area.name,
          device_id: device.id,
          device_name: device.name,
          device_kind: device.kind,
          state: entity.state,
          disabled: entity.disabled,
          hidden: entity.hidden,
        })
      }
    }
  }
  try { await Promise.all(Array.from({ length: Math.min(4, directory.areas.length) }, worker)); return entities }
  catch (cause) { controller.abort(); throw cause }
}
