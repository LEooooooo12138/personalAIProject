import { afterEach, expect, it, vi } from 'vitest'
import type { AreaDevices, AreaList, DeviceView, EntityView } from './api'
import { readAutomationEntityDirectory } from './automationEntityDirectory'

afterEach(() => vi.unstubAllGlobals())
const meta = { freshness: 'fresh', connection: 'connected', observed_at: null, last_attempt_at: null, last_success_at: null, error_code: null } as const
function areaList(ids: string[]): AreaList {
  return { meta, totals: { devices: 0, entities: 0, standalone_entities: 0 }, areas: ids.map((id) => ({ id, name: id === 'u_other' ? '其他' : `房间${id}`, device_count: 0, entity_count: 0, standalone_entity_count: 0, matches: [] })) }
}
function entity(entity_id: string, extra: Partial<EntityView> = {}): EntityView {
  return { entity_id, name: '存在感应', domain: 'binary_sensor', state: 'on', device_class: 'occupancy', unit: null, last_changed: null, last_updated: null, disabled: false, hidden: false, category: null, ...extra }
}
function devices(area_id: string, entities: EntityView[], extra: Partial<DeviceView> = {}): AreaDevices {
  return { meta, area: { id: area_id, name: area_id === 'u_other' ? '其他' : `房间${area_id}` }, devices: [{ id: 'd_shared', kind: 'device', name: '感应器', area_id, direct_area_id: 'a', membership: 'entity', entity_count: entities.length, domains: ['binary_sensor'], entities, load_location_verified: false, ...extra }] }
}

it('bounds parallel reads while projecting selection metadata without registry attributes', async () => {
  let active = 0, peak = 0
  vi.stubGlobal('fetch', async (url: string) => {
    if (url.endsWith('/areas')) return Response.json(areaList(Array.from({ length: 12 }, (_, i) => String(i))))
    active++; peak = Math.max(peak, active)
    await new Promise((resolve) => setTimeout(resolve, 1)); active--
    const id = url.split('/').at(-2)!
    return Response.json(devices(id, [{ ...entity(`binary_sensor.${id}`), attributes: { secret: 'never exported' } } as EntityView]))
  })
  const result = await readAutomationEntityDirectory(new AbortController().signal)
  expect(peak).toBe(4)
  expect(result.get('binary_sensor.0')).toEqual({ entity_id: 'binary_sensor.0', name: '存在感应', domain: 'binary_sensor', device_class: 'occupancy', state: 'on', disabled: false, hidden: false, area_id: '0', area_name: '房间0', device_id: 'd_shared', device_name: '感应器', device_kind: 'device' })
})

it('keeps cross-room entities in their effective API area and preserves standalone other entities', async () => {
  vi.stubGlobal('fetch', async (url: string) => {
    if (url.endsWith('/areas')) return Response.json(areaList(['a', 'b', 'u_other']))
    if (url.includes('/areas/a/')) return Response.json(devices('a', [entity('binary_sensor.a')]))
    if (url.includes('/areas/b/')) return Response.json(devices('b', [entity('binary_sensor.b'), entity('binary_sensor.hidden', { hidden: true }), entity('binary_sensor.disabled', { disabled: true })]))
    return Response.json(devices('u_other', [entity('group.solo', { domain: 'group', state: 'home' })], { id: 'e_solo', name: '独立存在组', kind: 'entity', direct_area_id: null }))
  })
  const result = await readAutomationEntityDirectory(new AbortController().signal)
  expect(result.get('binary_sensor.a')).toMatchObject({ area_id: 'a', device_id: 'd_shared' })
  expect(result.get('binary_sensor.b')).toMatchObject({ area_id: 'b', area_name: '房间b', device_id: 'd_shared' })
  expect(result.get('group.solo')).toMatchObject({ area_id: 'u_other', area_name: '其他', device_kind: 'entity' })
  expect(result.get('binary_sensor.hidden')).toMatchObject({ hidden: true })
  expect(result.has('binary_sensor.disabled')).toBe(false)
})

it.each(['stale', 'unknown'])('rejects %s directory metadata', async (freshness) => {
  vi.stubGlobal('fetch', async () => Response.json({ ...areaList([]), meta: { ...meta, freshness } }))
  await expect(readAutomationEntityDirectory(new AbortController().signal)).rejects.toMatchObject({ code: 'ha_unavailable' })
})

it('rejects unavailable area reads without returning a partial directory', async () => {
  vi.stubGlobal('fetch', async (url: string) => Response.json(url.endsWith('/areas') ? areaList(['a']) : { ...devices('a', [entity('binary_sensor.a')]), meta: { ...meta, connection: 'unavailable' } }))
  await expect(readAutomationEntityDirectory(new AbortController().signal)).rejects.toMatchObject({ code: 'ha_unavailable' })
})

it('does not start reads after cancellation', async () => {
  const controller = new AbortController(); controller.abort()
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
  await expect(readAutomationEntityDirectory(controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
  expect(fetcher).not.toHaveBeenCalled()
})

it('rejects cancellation during an empty directory response', async () => {
  const controller = new AbortController()
  vi.stubGlobal('fetch', async () => { controller.abort(); return Response.json(areaList([])) })
  await expect(readAutomationEntityDirectory(controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
})

it('does not start queued reads when cancellation happens during active requests', async () => {
  const controller = new AbortController()
  let started = 0
  let announceStarted!: () => void
  const allStarted = new Promise<void>((resolve) => { announceStarted = resolve })
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  vi.stubGlobal('fetch', async (url: string) => {
    if (url.endsWith('/areas')) return Response.json(areaList(['a', 'b', 'c', 'd', 'e', 'f']))
    if (++started === 4) announceStarted()
    await gate
    return Response.json(devices('a', []))
  })
  const read = readAutomationEntityDirectory(controller.signal)
  const rejected = expect(read).rejects.toMatchObject({ name: 'AbortError' })
  await allStarted
  controller.abort(); release()
  await rejected
  expect(started).toBe(4)
})
