import { useState } from 'react'
import { Link } from 'react-router-dom'
import { errorText, type AnalysisView, type CollectionView } from '../api'
import { useFamilyQuery } from '../familyQuery'
import { useConsoleAction } from '../consoleActions'
import { dateLabel } from '../familyViews'

export default function CollectionPage() {
  const collection = useFamilyQuery<CollectionView>('/admin/collection')
  const action = useConsoleAction()
  const [analysis, setAnalysis] = useState<AnalysisView | null>(null)
  async function analyze() {
    setAnalysis(null)
    const result = await action.run<AnalysisView>('/admin/analyze', { days: 14 })
    if (result) { setAnalysis(result); collection.reload() }
  }
  const data = collection.data
  const currentSnapshot = data?.snapshot_at && data.last_attempt_at && Date.parse(data.snapshot_at) >= Date.parse(data.last_attempt_at)
  return <main className="content-page collection-page"><Link className="return-link" to="/app/account">返回我的</Link><div className="page-intro"><h1>家庭数据采集</h1><p>显示服务端实际采集记录。刷新显示不会触发设备动作或额外采集。</p></div>
    {!data ? collection.error ? <div className="state-panel danger" role="alert"><p>{errorText(collection.error)}</p><button className="button button-secondary" onClick={collection.reload}>重新读取采集状态</button></div> : <p role="status">正在读取采集记录…</p> : <section className="panel collection-status"><h2>{data.phase === 'failed' ? currentSnapshot ? '历史补采失败' : '采集失败' : data.phase === 'snapshot' ? '正在保存状态快照' : data.phase === 'history' ? '正在补采历史' : '当前空闲'}</h2>{collection.error && <p role="alert" className="form-error">{errorText(collection.error)} 当前显示上次记录。</p>}<dl className="status-details"><div><dt>状态快照</dt><dd><span>{data.snapshot_at ? '状态快照已保存' : '尚无快照成功记录'}</span> · {dateLabel(data.snapshot_at)}</dd></div><div><dt>完整采集</dt><dd>{data.last_success_at ? dateLabel(data.last_success_at) : '尚无完整采集成功记录'}</dd></div><div><dt>最近尝试</dt><dd>{dateLabel(data.last_attempt_at)}</dd></div><div><dt>最近失败</dt><dd>{dateLabel(data.last_failure_at)}</dd></div><div><dt>当前窗口</dt><dd>{dateLabel(data.window_start)} 至 {dateLabel(data.window_end)}</dd></div><div><dt>窗口批次</dt><dd>{data.completed_batches} / {data.total_batches} 批</dd></div><div><dt>磁盘 checkpoint</dt><dd>{dateLabel(data.checkpoint)}</dd></div></dl><p className="panel-description">checkpoint 只代表已保存进度，不能证明本进程采集成功。</p>{data.error_code && <p className="state-panel warning">{errorTextForCode(data.error_code)}</p>}<button className="button button-secondary" onClick={collection.reload}>刷新采集显示</button></section>}
    <section className="panel analysis-panel"><h2>分析习惯</h2><p>仅分析最近 14 天并产生待审核建议，不安装自动化。</p><button className="button button-primary" disabled={action.pending} onClick={() => void analyze()}>{action.pending ? '分析中…' : '分析最近 14 天'}</button>{action.error && <p className="form-error" role="alert">{action.error}</p>}{analysis && <p role="status">分析完成，产生 {analysis.suggestion_count} 条建议；尚未安装自动化。</p>}<Link className="text-link" to="/app/automations">查看并审核建议</Link></section>
  </main>
}
function errorTextForCode(code: string) { const labels: Record<string, string> = { ha_auth_required: 'HA 服务授权失效，请由管理员处理。', ha_forbidden: 'HA 服务读取权限不足。', ha_timeout: 'HA 历史请求超时。', ha_unavailable: 'HA 暂时无法连接。', ha_invalid_response: 'HA 返回的数据暂时无法使用。', ha_not_configured: '尚未配置 HA 服务。' }; return labels[code] ?? '本轮采集未完成，请管理员核对服务。' }