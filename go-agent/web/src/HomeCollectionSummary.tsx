import { Link } from 'react-router-dom'
import { errorText, type CollectionView } from './api'
import { useAuth } from './auth'
import { useFamilyQuery } from './familyQuery'
import { dateLabel } from './familyViews'
import { Icon } from './icons'

export default function HomeCollectionSummary() {
  const auth = useAuth()
  const enabled = auth.identity?.user.role === 'admin' && auth.identity.capabilities.includes('collection:read')
  const collection = useFamilyQuery<CollectionView>('/admin/collection', enabled)
  if (!enabled) return null
  const data = collection.data
  const phase = data?.phase === 'failed' ? '本轮采集未完成，请查看详情。'
    : data?.phase === 'snapshot' ? '正在保存状态快照'
    : data?.phase === 'history' ? '正在补采历史'
    : '当前空闲'
  return <section className="panel home-collection-summary">
    <h2>采集状态与异常</h2>
    {collection.error ? <p role="alert">{errorText(collection.error)} 采集摘要暂无法核对。</p>
      : !data ? <p role="status">正在读取采集摘要…</p>
      : <><p role="status">{phase}</p><p>{data.last_success_at ? <>最近完整采集：{dateLabel(data.last_success_at)}</> : '尚无本进程完整采集成功记录'}</p></>}
    <Link className="text-link" to="/app/admin/collection">采集详情与分析<Icon name="arrow"/></Link>
  </section>
}
