import { Link } from 'react-router-dom'
import type { AreaList, IntegrationsStatus } from './api'
import { useAuth } from './auth'
import { useFamilyQuery } from './familyQuery'
import { dateLabel } from './familyViews'
import { integrationStatusItems } from './homeStatus'
import { Alert, Button, Card, Disclosure, Grid, Icon, KeyValueList, LoadingState, Metric, Section, Stack, StatusBadge } from './ui'

export default function HomeFamilySummary() {
  const auth = useAuth()
  const enabled = auth.identity?.capabilities.includes('areas:read') ?? false
  const areas = useFamilyQuery<AreaList>('/areas', enabled)
  const integrations = useFamilyQuery<IntegrationsStatus>('/integrations/status', enabled)
  if (!enabled) return null
  const status = integrations.data
  const facts = integrationStatusItems(status, Boolean(integrations.error))
  return <Section title="家庭概况" actions={<Link className="jx-action-link" to="/app/areas">查看全部区域<Icon name="arrow"/></Link>}>
    {areas.data ? <><Grid columns={3} gap={16}><Metric label="区域" value={areas.data.areas.length} unit="个"/><Metric label="设备注册项" value={areas.data.totals.devices} unit="个"/><Metric label="实体" value={areas.data.totals.entities} unit="个"/></Grid><p className="jx-muted">{areas.error || areas.data.meta.freshness === 'stale' ? '旧目录 · ' : ''}数据时间 {dateLabel(areas.data.meta.observed_at)}</p></> : areas.error ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={areas.reload}>重新读取家庭目录</Button>}>家庭目录暂时无法读取。</Alert> : <LoadingState label="正在读取家庭目录…"/>}
    <section aria-label="本机服务状态"><Stack gap={16}><h3>本机服务状态</h3>
      {!status ? integrations.error ? <Alert tone="danger" live="assertive">服务状态暂时不可用。</Alert> : <LoadingState label="正在检查本机服务…"/> : <Grid columns={2} gap={16}>
        <Card heading="家庭设备 · HA"><Stack gap={12}><StatusBadge tone={facts[0].tone} label={facts[0].label}/><StatusBadge tone={facts[1].tone} label={facts[1].label}/><p>{facts[0].detail}</p><p className="jx-muted">{facts[1].detail}</p><Disclosure title="设备服务检查详情"><KeyValueList items={[{ id: 'attempt', label: '检查时间', value: dateLabel(status.ha.last_attempt_at) }, { id: 'observed', label: '数据时间', value: dateLabel(status.ha.observed_at) }]}/></Disclosure></Stack></Card>
        <Card heading="本机 AI · Ollama"><Stack gap={12}><StatusBadge tone={facts[2].tone} label={facts[2].label}/><StatusBadge tone={facts[3].tone} label={facts[3].label}/><p className="jx-muted">{facts[3].detail}</p><Disclosure title="模型安装与检查详情"><p>检查时间：{dateLabel(status.ollama.checked_at)}</p>{status.ollama.models.length ? <ul>{status.ollama.models.map((model) => <li key={model.name}>{model.name} {status.ollama.connection !== 'connected' || integrations.error || model.available === null ? '暂无法确认' : model.available ? '已安装' : '未安装'}</li>)}</ul> : <p>暂无法确认模型安装情况。</p>}</Disclosure></Stack></Card>
      </Grid>}
      <Button variant="secondary" onClick={integrations.reload}><Icon name="refresh"/>重新检查服务</Button>
    </Stack></section>
  </Section>
}
