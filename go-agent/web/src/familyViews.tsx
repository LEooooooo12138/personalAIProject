import { Link } from 'react-router-dom'
import { ApiError, errorText, type CatalogMeta, type DeviceView, type EntityView } from './api'
import { Icon } from './icons'

export function dateLabel(value: string | null): string {
  if (!value) return '暂无记录'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '暂无记录' : new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
}
export function areaPath(id: string) { return `/app/areas/${encodeURIComponent(id)}` }
export function devicePath(areaId: string, id: string) { return `${areaPath(areaId)}/devices/${encodeURIComponent(id)}` }
export function haStatusText(meta: CatalogMeta, failed = false) {
  const causes: Record<string, string> = { ha_auth_required: '家庭设备服务授权失效，请联系管理员。', ha_forbidden: '家庭设备服务权限不足，请联系管理员。', ha_timeout: '家庭设备服务请求超时，请稍后重新检查。', ha_invalid_response: '家庭设备服务返回异常，请联系管理员。', ha_not_configured: '家庭设备服务尚未配置。', ha_unavailable: 'HA 暂时无法连接' }
  if (meta.error_code && causes[meta.error_code]) return causes[meta.error_code]
  return failed || meta.connection === 'unavailable' ? 'HA 暂时无法连接' : meta.connection === 'connected' ? 'HA 最近检查成功' : 'HA 连接尚未确认'
}
export function domainLabel(domain: string) { const names: Record<string, string> = { light: '灯', switch: '开关', fan: '风扇', binary_sensor: '二值传感器', sensor: '传感器', cover: '窗帘', climate: '温控', scene: '场景', group: '实体分组', person: '人员状态', device_tracker: '设备追踪', input_boolean: '布尔辅助项', automation: '自动化' }; return names[domain] ? `${names[domain]}（${domain}）` : domain }
export function FamilyStatus({ meta, failed = false, reload }: { meta: CatalogMeta; failed?: boolean; reload: () => void }) {
  const stale = failed || meta.freshness === 'stale'
  return <section className={`family-status ${stale || meta.connection === 'unavailable' ? 'family-status-warning' : ''}`} aria-label="HA 与目录状态">
    <div className="family-status-head"><strong><span className={`service-dot ${!failed && meta.connection === 'connected' ? 'connected' : 'unavailable'}`}/>{haStatusText(meta, failed)}</strong><button className="text-button" onClick={reload}>重新检查</button></div>
    {stale ? <p className="stale-note" role="status">正在显示旧数据</p> : meta.freshness === 'unknown' ? <p className="stale-note">尚无完整目录</p> : null}
    <div className="catalog-times"><span>数据时间 <time>{dateLabel(meta.observed_at)}</time></span><span>检查时间 <time>{dateLabel(meta.last_attempt_at)}</time></span><span>最近成功 <time>{dateLabel(meta.last_success_at)}</time></span></div>
  </section>
}
export function FamilyPending() { return <div className="empty-card" role="status"><span className="loader"/>正在读取家庭目录…</div> }
export function FamilyFailure({ error, reload }: { error: ApiError | null; reload: () => void }) {
  return <div className="empty-card error-state" role="alert"><strong>{error?.status === 404 ? '未找到这个区域或设备。' : '家庭目录暂时不可用。'}</strong><p>{error ? errorText(error) : '请稍后重新加载。'}</p><button className="button button-secondary" onClick={reload}>重新加载</button></div>
}
export function FamilyBreadcrumb({ area, device }: { area?: { id: string; name: string }; device?: string }) {
  return <nav className="family-breadcrumb" aria-label="页面路径"><Link to="/app/areas">全部区域</Link>{area && <><Icon name="chevron" size={15}/>{device ? <Link to={areaPath(area.id)}>{area.name}</Link> : <span aria-current="page">{area.name}</span>}</>}{device && <><Icon name="chevron" size={15}/><span aria-current="page">{device}</span></>}</nav>
}
export function membershipLabel(device: DeviceView) {
  return device.membership === 'entity' ? '实体覆盖到此区域' : device.membership === 'both' ? '直接归属与实体关系' : '直接归属此区域'
}
export function DeviceCard({ device, areaId }: { device: DeviceView; areaId: string }) {
  return <Link className="device-card" to={devicePath(areaId, device.id)}><span className="device-card-icon"><Icon name={device.kind === 'device' ? 'device' : 'sensor'} size={23}/></span><div className="device-card-main"><h3>{device.name}</h3><p>{device.kind === 'device' ? '设备注册项' : '独立实体'} · {device.entity_count} 个实体</p><span className="membership-tag">{membershipLabel(device)}</span>{device.entity_count === 0 && <small>此区域无该设备的实体</small>}</div><Icon name="chevron" size={19}/></Link>
}
export function entityStateLabel(entity: EntityView) {
  const state = entity.state
  if (state === null) return '暂无状态'
  if (state === 'unavailable') return '不可用'
  if (state === 'unknown') return '状态未知'
  if (state !== 'on' && state !== 'off') return state
  const active = state === 'on'
  if (['light', 'switch', 'fan', 'input_boolean'].includes(entity.domain)) return active ? '开启' : '关闭'
  if (entity.domain === 'binary_sensor') {
    if (['door', 'window', 'opening'].includes(entity.device_class ?? '')) return active ? '打开' : '关闭'
    if (entity.device_class === 'tamper') return active ? '检测到防拆' : '未检测到防拆'
    if (['motion', 'occupancy', 'presence'].includes(entity.device_class ?? '')) return active ? '检测到' : '未检测到'
  }
  return state
}
export function EntityCard({ entity }: { entity: EntityView }) {
  return <article className="entity-card"><div className="entity-heading"><h3>{entity.name}</h3><span className="domain-tag">{domainLabel(entity.domain)}</span></div><div className="entity-state"><strong>{entityStateLabel(entity)}</strong>{entity.state !== null && entity.state !== 'unavailable' && entity.state !== 'unknown' && entity.unit !== null && <span>{entity.unit}</span>}</div><div className="entity-flags">{entity.disabled && <span>已禁用</span>}{entity.hidden && <span>已隐藏</span>}</div><dl><div><dt>状态变化</dt><dd><time>{dateLabel(entity.last_changed)}</time></dd></div><div><dt>最近更新</dt><dd><time>{dateLabel(entity.last_updated)}</time></dd></div></dl><details className="entity-technical"><summary>技术详情</summary><p><code>{entity.entity_id}</code></p><p>原始状态：{entity.state ?? 'null'}</p><p>类别：{entity.device_class ?? '暂无类别'}</p></details></article>
}