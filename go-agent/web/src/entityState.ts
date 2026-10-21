export type EntityStateInput = { state: string | null; domain?: string | null; device_class?: string | null }

export function formatEntityState({ state, domain, device_class }: EntityStateInput): string {
  if (state === null) return '暂无状态'
  if (state === 'unavailable') return '不可用'
  if (state === 'unknown') return '状态未知'
  if (state !== 'on' && state !== 'off') return state
  const active = state === 'on'
  if (['light', 'switch', 'fan', 'input_boolean'].includes(domain ?? '')) return active ? '开启' : '关闭'
  if (domain === 'binary_sensor') {
    if (['door', 'window', 'opening'].includes(device_class ?? '')) return active ? '打开' : '关闭'
    if (device_class === 'tamper') return active ? '检测到防拆' : '未检测到防拆'
    if (['motion', 'occupancy', 'presence'].includes(device_class ?? '')) return active ? '检测到' : '未检测到'
  }
  return state
}
