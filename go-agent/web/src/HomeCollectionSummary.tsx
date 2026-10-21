import { Link } from 'react-router-dom'
import { errorText, type CollectionView } from './api'
import { useAuth } from './auth'
import { useFamilyQuery } from './familyQuery'
import { dateLabel } from './familyViews'
import { collectionPresentation, hasCollectionTimestamp } from './collectionPresentation'
import { Alert, Icon, LoadingState, Section, StatusBadge } from './ui'
export default function HomeCollectionSummary() {
  const auth = useAuth()
  const enabled = auth.identity?.user.role === 'admin' && auth.identity.capabilities.includes('collection:read')
  const collection = useFamilyQuery<CollectionView>('/admin/collection', enabled)
  if (!enabled) return null
  const data = collection.data
  return <Section title="采集状态与异常" actions={<Link className="jx-action-link" to="/app/admin/collection">采集详情与分析<Icon name="arrow"/></Link>}>
    {collection.error ? <Alert tone="warning" live="assertive">{errorText(collection.error)} 采集摘要暂无法核对。</Alert> : !data ? <LoadingState label="正在读取采集摘要…"/> : <><StatusBadge tone={data.phase === 'failed' ? 'danger' : data.phase === 'idle' ? 'neutral' : 'info'} label={collectionPresentation(data).title}/>{data.phase === 'failed' && <p>本轮采集未完成，请查看详情。</p>}<p>{hasCollectionTimestamp(data.last_success_at) ? <>最近完整采集：{dateLabel(data.last_success_at)}</> : '尚无本进程完整采集成功记录'}</p></>}
  </Section>
}
