import { Link } from 'react-router-dom'
import type { AreaList, IntegrationsStatus } from './api'
import { useAuth } from './auth'
import { useFamilyQuery } from './familyQuery'
import { dateLabel, FamilyFailure, FamilyPending, haStatusText } from './familyViews'
import { Icon } from './icons'

export default function HomeFamilySummary() {
  const auth = useAuth()
  const enabled = auth.identity?.capabilities.includes('areas:read') ?? false
  const areas = useFamilyQuery<AreaList>('/areas', enabled)
  const integrations = useFamilyQuery<IntegrationsStatus>('/integrations/status', enabled)
  if (!enabled) return null
  const status = integrations.data
  const ollama = status?.ollama
  return <section className="home-family"><div className="section-heading"><h2>家庭概况</h2><Link className="text-link" to="/app/areas">查看全部区域<Icon name="arrow"/></Link></div>
    <section className="integration-panel panel" aria-label="本机服务状态"><h3>本机服务状态</h3>{!status ? integrations.error ? <><p className="form-error" role="alert">服务状态暂时不可用。</p><button className="button button-secondary" onClick={integrations.reload}>重新检查</button></> : <p role="status">正在检查本机服务…</p> : <div className="service-grid"><div className="service-card"><strong>家庭设备 · HA</strong><p>{haStatusText(status.ha, Boolean(integrations.error))}</p>{status.ha.freshness === 'stale' && <p className="stale-note">正在显示旧数据</p>}<p>检查时间：{dateLabel(status.ha.last_attempt_at)}</p><p>数据时间：{dateLabel(status.ha.observed_at)}</p></div><div className="service-card"><strong>本机 AI · Ollama</strong><p>{ollama?.connection === 'connected' && !integrations.error ? '最近检查成功' : ollama?.connection === 'unavailable' || integrations.error ? '暂时无法连接' : '尚未确认'}</p><p>检查时间：{dateLabel(ollama?.checked_at ?? null)}</p>{ollama && ollama.models.length > 0 ? <ul className="model-checks">{ollama.models.map((model) => <li key={model.name}>{model.name} {ollama.connection !== 'connected' || integrations.error || model.available === null ? '暂无法确认' : model.available ? '已安装' : '未安装'}</li>)}</ul> : <p>暂无法确认模型安装情况。</p>}<p className="model-disclaimer">安装检查不代表实际生成已验证。</p></div></div>}<button className="button button-secondary" onClick={integrations.reload}><Icon name="refresh"/>重新检查服务</button></section>
    {!areas.data ? areas.error ? <FamilyFailure error={areas.error} reload={areas.reload}/> : <FamilyPending/> : <Link className="family-summary-link" to="/app/areas"><span className="summary-home-icon"><Icon name="grid" size={24}/></span><div><strong>{areas.data.areas.length} 个区域</strong><p>{areas.data.totals.devices} 个设备注册项 · {areas.data.totals.entities} 个实体</p><small>{areas.error || areas.data.meta.freshness === 'stale' ? '旧目录 · ' : ''}数据时间 {dateLabel(areas.data.meta.observed_at)}</small></div><Icon name="arrow"/></Link>}
  </section>
}