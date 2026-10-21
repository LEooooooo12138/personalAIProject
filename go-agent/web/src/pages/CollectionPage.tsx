import { Alert, Breadcrumb, Button, Card, Disclosure, Icon, KeyValueList, LoadingState, PageFrame, PageHeader, ProgressSummary, Stack, StepSequence, ActionGroup, StatusBadge } from '../ui'
import { collectionPresentation, hasCollectionTimestamp } from '../collectionPresentation'
import { Link } from 'react-router-dom'
import { errorText, type AnalysisRun, type BackfillRun, type CollectionView, type HistoryCoverage, type PatternOutcome } from '../api'
import { useFamilyQuery } from '../familyQuery'
import { terminalRun, useAnalysisRun } from '../useAnalysisRun'
import { dateLabel } from '../familyViews'

function parseTargetInstant(value: unknown): number {
 if (typeof value !== 'string') return NaN
 const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.exec(value)
 if (!parts) return NaN
 const [year, month, day, hour, minute, second] = parts.slice(1, 7).map(Number)
 // Date parsing normalizes invalid dates and 24:00. Verify the original
 // calendar components before interpreting the explicit timezone offset.
 const calendar = new Date(0)
 calendar.setUTCFullYear(year, month - 1, day)
 calendar.setUTCHours(hour, minute, second, 0)
 if (calendar.getUTCFullYear() !== year || calendar.getUTCMonth() !== month - 1 || calendar.getUTCDate() !== day
  || calendar.getUTCHours() !== hour || calendar.getUTCMinutes() !== minute || calendar.getUTCSeconds() !== second) return NaN
 return Date.parse(value)
}
export default function CollectionPage() {
 const collection = useFamilyQuery<CollectionView>('/admin/collection')
 const data = collection.data
 const analysis = useAnalysisRun<AnalysisRun>('analysis', data?.latest_analysis, data?.analysis_runs)
 const backfill = useAnalysisRun<BackfillRun>('backfill', data?.backfill_runs?.[0], data?.backfill_runs)
 function refreshCollection() { analysis.refresh(); backfill.refresh(); collection.reload() }
 const targetWindow = data?.requested_window
 const targetStart = parseTargetInstant(targetWindow?.start)
 const targetEnd = parseTargetInstant(targetWindow?.end)
 const targetWindowError = !Number.isFinite(targetStart) || !Number.isFinite(targetEnd) || targetEnd <= targetStart
  ? '目标窗口暂无法确认，请刷新采集显示后重试。'
  : targetEnd - targetStart > 14 * 86400000
   ? '目标窗口超过单次 14 × 24 小时的补采限制，暂不能补采此完整窗口；请核对家庭时区及窗口。'
   : ''
 async function startBackfill() {
  if (!targetWindow || targetWindowError) return
  await backfill.start(targetWindow)
 }
 return <PageFrame className="collection-page">
  <Breadcrumb><Link to="/app/account">返回我的</Link></Breadcrumb>
  <PageHeader title="家庭数据采集" description="显示服务端实际采集记录。刷新显示不会触发设备动作或额外采集。"/>
  {!data ? collection.error ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={refreshCollection}>重新读取采集状态</Button>}>{errorText(collection.error)}</Alert> : <LoadingState label="正在读取采集记录…"/> : <Card heading={collectionPresentation(data).title}>
   <Stack gap={24}>
    {collection.error && <Alert tone="warning" live="assertive">{errorText(collection.error)} 当前显示上次记录。</Alert>}
    <StepSequence label="本轮采集流程" steps={collectionPresentation(data).steps}/>
    <ProgressSummary label="窗口批次" completed={data.completed_batches} total={data.total_batches} unit="批"/>
    <KeyValueList items={[{ id: 'snapshot', label: '状态快照', value: <><span>{hasCollectionTimestamp(data.snapshot_at) ? '状态快照已保存' : '尚无快照成功记录'}</span> · {dateLabel(data.snapshot_at)}</> }, { id: 'success', label: '完整采集', value: hasCollectionTimestamp(data.last_success_at) ? dateLabel(data.last_success_at) : '尚无完整采集成功记录' }, { id: 'window', label: '当前窗口', value: dateLabel(data.window_start) + ' 至 ' + dateLabel(data.window_end) }, { id: 'next', label: '下一次计划', value: data.next_analysis_at ? dateLabel(data.next_analysis_at) : data.next_analysis_reason === 'scheduler_inactive' ? '调度器未运行' : '暂无法确认下一次计划时间' }]}/>
    {data.error_code && <Alert tone="warning">{errorTextForCode(data.error_code)}</Alert>}
    <Disclosure title="采集记录与 checkpoint"><p>checkpoint 只代表已保存进度，不能证明本进程采集成功。</p><KeyValueList items={[{ id: 'attempt', label: '最近尝试', value: dateLabel(data.last_attempt_at) }, { id: 'failure', label: '最近失败', value: dateLabel(data.last_failure_at) }, { id: 'checkpoint', label: '磁盘 checkpoint', value: dateLabel(data.checkpoint) }]}/></Disclosure>
    <Button variant="secondary" onClick={refreshCollection}><Icon name="refresh"/>刷新采集显示</Button>
   </Stack>
  </Card>}
  <Card heading="数据覆盖" description="分析目标 14 天。请求成功范围与实体实际观测分别计算。">
   <Stack gap={16}>
    {data?.requested_window && <p>目标窗口：{dateLabel(data.requested_window.start)} 至 {dateLabel(data.requested_window.end)}</p>}
    {data?.coverage ? <CoverageView coverage={data.coverage}/> : <p>覆盖范围暂无法确认；{data?.coverage_error ? '本次覆盖读取未完成。' : '尚无覆盖记录。'}</p>}
    <p>HA recorder 保留能力未知。请求成功或空响应都不能证明完整保留了历史。</p>
   </Stack>
  </Card>
  <Card heading="分析习惯" description="仅分析最近 14 天并产生待审核建议，不安装自动化。">
   <Stack gap={16}>
    <p>时间模式要求至少 7 个命中日，且占请求天数至少 75%；14 天窗口需至少 11 个命中日。不会因数据不足降低门槛。</p>
    <ActionGroup><Button busy={analysis.pending} disabled={analysis.active || analysis.uncertain} onClick={() => void analysis.start({ days: 14 })}>{analysis.pending ? '正在提交分析…' : '分析现有数据'}</Button>{analysis.active && <Button variant="secondary" disabled={analysis.pending || analysis.cancelRequested} onClick={() => void analysis.cancel()}>取消分析</Button>}</ActionGroup>
    {analysis.error && <Alert tone="danger" live="assertive">{analysis.error}</Alert>}
    {analysis.cancelRequested && !terminalRun(analysis.status) && <Alert tone="info" live="polite">已请求取消，等待任务状态确认。</Alert>}
    {analysis.data ? <AnalysisResult run={analysis.data}/> : analysis.id && <LoadingState label="分析任务已受理，正在读取状态…"/>}
    <Link className="jx-action-link" to="/app/automations">查看并审核建议<Icon name="arrow"/></Link>
   </Stack>
  </Card>
  <Card heading="历史补采" description="管理员明确启动后，仅请求最多 14 天的设备历史；补采不会自动启动分析。">
   <Stack gap={16}>
    <ActionGroup><Button variant="secondary" busy={backfill.pending} disabled={backfill.active || backfill.uncertain || Boolean(targetWindowError)} onClick={() => void startBackfill()}>补采最近 14 天</Button>
     {backfill.data && (backfill.status === 'failed' || backfill.status === 'cancelled') && <Button variant="secondary" disabled={backfill.pending} onClick={() => void backfill.start(backfill.data!.requested_window)}>继续原窗口补采</Button>}
     {backfill.active && <Button variant="secondary" disabled={backfill.pending || backfill.cancelRequested} onClick={() => void backfill.cancel()}>取消补采</Button>}
    </ActionGroup>
    {data && targetWindowError && <Alert tone="warning" live="polite">{targetWindowError}</Alert>}
    {backfill.error && <Alert tone="danger" live="assertive">{backfill.error}</Alert>}
    {backfill.cancelRequested && !terminalRun(backfill.status) && <Alert tone="info" live="polite">已请求取消补采，等待任务状态确认。</Alert>}
    {backfill.data ? <><StatusBadge label={'补采' + statusLabel(backfill.data.status)} tone={backfill.data.status === 'failed' ? 'warning' : 'neutral'}/><KeyValueList items={[{ id: 'window', label: '补采窗口', value: dateLabel(backfill.data.requested_window.start) + ' 至 ' + dateLabel(backfill.data.requested_window.end) }, { id: 'batches', label: '成功请求批次', value: backfill.data.completed_batches }, { id: 'gaps', label: '尚未完成的请求窗口', value: backfill.data.gaps?.length ?? '未知' }]}/>{backfill.data.error_code && <Alert tone="warning">{errorTextForCode(backfill.data.error_code)}</Alert>}</> : backfill.id && <LoadingState label="补采任务已受理，正在读取状态…"/>}
    <p>取消不会清空已保存记录。中断后仅在明确选择“继续原窗口补采”时恢复。</p>
   </Stack>
  </Card>
  <Card heading="最近分析记录" description="显示当前管理员与家庭计划任务的最近 10 条记录。">
   {!data?.analysis_runs?.length ? <p>暂无可读取的分析记录。</p> : <Stack gap={16}>{data.analysis_runs.map(run => <Disclosure key={run.id} title={dateLabel(run.created_at) + ' · ' + statusLabel(run.status) + (run.source === 'scheduled' ? ' · 家庭计划' : '')}><AnalysisResult run={run}/></Disclosure>)}</Stack>}
  </Card>
 </PageFrame>
}
function days(duration: number) {
 if (!Number.isFinite(duration) || duration < 0) return '未知'
 if (duration === 0) return '0 天'
 const value = duration / 86400000000000
 return value < 0.01 ? '不足 0.01 天' : '约 ' + value.toFixed(value < 1 ? 2 : 1) + ' 天'
}
function CoverageView({ coverage }: { coverage: HistoryCoverage }) {
 return <><KeyValueList items={[{ id: 'requested', label: '已请求窗口并集', value: days(coverage.requested_union_duration) }, { id: 'success', label: '成功请求窗口并集', value: days(coverage.successful_union_duration) }, { id: 'corrupt', label: '不可读取的历史文件', value: coverage.corrupt_files }]}/>
  <Disclosure title="逐实体观测质量">{Object.entries(coverage.entities ?? {}).length === 0 ? <p>尚无有效实体观测；不能据此判断没有规律。</p> : <Stack gap={16}>{Object.entries(coverage.entities).map(([id, quality]) => <div key={id}><code>{id}</code><KeyValueList items={[{ id: 'samples', label: '有效状态观测', value: quality.valid_state_count }, { id: 'changes', label: '已核实状态变化', value: quality.change_count }, { id: 'success', label: '成功请求范围', value: days(quality.successful_union_duration) }, { id: 'gaps', label: '请求缺口', value: quality.gaps?.length ?? '未知' }]}/></div>)}</Stack>}</Disclosure></>
}
function statusLabel(status: string) { return ({ queued: '排队中', running: '进行中', completed: '已完成', failed: '失败', cancelled: '已取消' } as Record<string, string>)[status] ?? '状态未知' }
const outcomeLabels: Record<string, string> = { matched: '发现符合门槛的规律', insufficient_data: '数据不足，当前算法无法给出结论', no_pattern: '未发现符合当前门槛的规律' }
const reasons: Record<string, string> = { no_eligible_entities: '没有适用实体', insufficient_observed_days: '有效观测日期不足', observed_days_sufficient_no_match: '观测日期满足前提，命中未达门槛', insufficient_source_transition_days: '源实体变化日期不足', insufficient_target_observation_days: '关联实体的重叠观测日期不足', source_opportunities_sufficient_no_match: '关联机会满足前提，未达门槛', insufficient_numeric_samples: '数值样本不足', numeric_samples_sufficient_no_match: '数值样本满足前提，未发现异常', detector_matched: '符合检测门槛', observed_prerequisites_satisfied: '观测前提满足', candidate_data_insufficient: '部分实体观测不足' }
function PatternResult({ name, result }: { name: string; result: PatternOutcome }) {
 return <div><h3>{({ time: '时间规律', correlation: '关联规律', anomaly: '数值异常' } as Record<string, string>)[name] ?? '其他模式'}</h3><p>{result.applicable ? outcomeLabels[result.outcome] ?? '结果未知' : '不适用于当前实体'}：{reasons[result.reason] ?? '原因暂无法确认'}</p>
 {(result.candidates ?? []).map((candidate, i) => <p key={candidate.entity_id + ':' + i}><code>{candidate.entity_id}</code>{candidate.related_id && <> → <code>{candidate.related_id}</code></>}：{reasons[candidate.reason] ?? '原因暂无法确认'}；观测 {candidate.observation_days} 天，源变化 {candidate.source_transitions} 次，数值样本 {candidate.numeric_samples} 个。</p>)}</div>
}
function AnalysisResult({ run }: { run: AnalysisRun }) {
 return <Stack gap={16}>
  <StatusBadge label={'分析' + statusLabel(run.status)} tone={run.status === 'failed' ? 'warning' : 'neutral'}/>
  {run.status === 'completed' && <Alert tone={run.outcome === 'insufficient_data' ? 'warning' : 'info'} live="polite">{outcomeLabels[run.outcome ?? ''] ?? '分析结果暂无法确认'}。本次建议 {run.counts?.suggestions ?? '未知'} 条；尚未安装自动化。</Alert>}
  {run.status === 'failed' && <Alert tone="warning">{errorTextForCode(run.error_code ?? 'analysis_failed')}</Alert>}
  <KeyValueList items={[{ id: 'window', label: '分析窗口', value: dateLabel(run.requested_window.start) + ' 至 ' + dateLabel(run.requested_window.end) }, { id: 'events', label: '有效期内观测条目', value: run.counts?.entries ?? '未知' }, { id: 'patterns', label: '检测模式数量', value: run.counts?.patterns ?? '未知' }]}/>
  {Object.entries(run.outcomes ?? {}).map(([name, result]) => <PatternResult key={name} name={name} result={result}/>)}
  <Disclosure title="任务记录详情"><KeyValueList items={[{ id: 'id', label: '任务编号', value: run.id }, { id: 'algorithm', label: '算法版本', value: run.algorithm_version }, { id: 'timezone', label: '家庭时区', value: run.home_timezone }, { id: 'created', label: '创建时间', value: dateLabel(run.created_at) }, { id: 'finished', label: '结束时间', value: dateLabel(run.finished_at ?? null) }]}/></Disclosure>
 </Stack>
}
function errorTextForCode(code: string) { const labels: Record<string, string> = { ha_auth_required: 'HA 服务授权失效，请由管理员处理。', ha_forbidden: 'HA 服务读取权限不足。', ha_timeout: 'HA 历史请求超时。', ha_unavailable: 'HA 暂时无法连接。', ha_invalid_response: 'HA 返回的数据暂时无法使用。', ha_not_configured: '尚未配置 HA 服务。', interrupted: '任务中断，需明确重新启动。', cancelled: '任务已取消，已保存记录仍保留。', analysis_failed: '分析未完成，请刷新记录核对。', analysis_store_failed: '分析记录保存失败，请核对存储。' }; return labels[code] ?? '本轮任务未完成，请管理员核对服务。' }
