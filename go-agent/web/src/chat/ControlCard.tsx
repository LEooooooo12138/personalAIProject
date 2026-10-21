import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Card, Disclosure, KeyValueList, StatusBadge, type StatusTone } from '../ui'
import { deviceStateText, observedTime, type ControlProposal, type DeviceResult } from './control-types'

type Props = {
  proposal: ControlProposal
  canControl: boolean
  error?: string
  refreshing?: boolean
  refreshError?: string
  onRefresh?: (id: string) => Promise<void>
  onConfirm: (id: string) => Promise<void>
  onCancel: (id: string) => Promise<void>
  onReconcile: (id: string) => Promise<void>
}
const statusText = { pending: '等待确认', executing: '正在执行，无法撤回', succeeded: '已观察到目标状态', failed: '操作未完成，请重新提出需求', unknown: '结果尚未确认，请核对状态', cancelled: '提议已取消', expired: '提议已过期，请重新提出需求' }
function StateUnit({ result }: { result: DeviceResult }) {
  return result.unit && !['unknown', 'unavailable'].includes(result.state) ? <span> {result.unit}</span> : null
}
export function DeviceResultCard({ result }: { result: DeviceResult }) {
 return <section className="control-card device-result" aria-label={`${result.name}设备状态`}><Card>
  <header><strong>{result.name}</strong><StatusBadge tone="info" label="设备查询"/></header>
  <p className="control-current">当前状态：{deviceStateText(result)}<StateUnit result={result}/></p>
  <p>观察时间：{observedTime(result.observed_at)}</p>
  <Disclosure title="技术详情"><KeyValueList items={[{ id: 'entity', label: '实体', value: result.entity_id }, { id: 'raw', label: '原始状态', value: result.state }, { id: 'class', label: '类别', value: result.device_class ?? '暂无类别' }]}/></Disclosure>
 </Card></section>
}
export default function ControlCard({ proposal, canControl, error, refreshing = false, refreshError, onRefresh, onConfirm, onCancel, onReconcile }: Props) {
  const [now, setNow] = useState(Date.now)
  const [busy, setBusy] = useState(false)
  const running = useRef(false)
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  useEffect(() => {
    if (proposal.status !== 'pending') return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 500)
    return () => window.clearInterval(timer)
  }, [proposal.status, proposal.expires_at])
  const expiry = Date.parse(proposal.expires_at)
  const expiryLatch = useRef({ key: '', expired: false })
  const expiryKey = `${proposal.id}:${proposal.expires_at}`
  if (expiryLatch.current.key !== expiryKey) expiryLatch.current = { key: expiryKey, expired: false }
  if (!Number.isFinite(expiry) || now >= expiry) expiryLatch.current.expired = true
  const expired = expiryLatch.current.expired
  const action = proposal.action === 'turn_on' ? '打开' : '关闭'
  async function invoke(callback: (id: string) => Promise<void>, confirming = false) {
    if (running.current || (confirming && (expired || !Number.isFinite(expiry) || Date.now() >= expiry))) return
    running.current = true; setBusy(true)
    try { await callback(proposal.id) } finally { running.current = false; if (mounted.current) setBusy(false) }
  }
  const tone: StatusTone = proposal.status === 'succeeded' ? 'success' : ['unknown', 'expired'].includes(proposal.status) || (proposal.status === 'pending' && expired) ? 'warning' : proposal.status === 'failed' ? 'danger' : proposal.status === 'executing' ? 'info' : 'neutral'
  return <section className="control-card" aria-label={`${proposal.name}控制提议`} aria-busy={busy}><Card tone={tone === 'warning' || tone === 'danger' || tone === 'info' ? tone : 'neutral'}>
    <header><strong>{proposal.name}</strong><span>{proposal.area_name || '未标注房间'}</span></header>
    <div className="control-state-flow">
     {proposal.before && <p className="control-current">当前状态：{deviceStateText(proposal.before)}<StateUnit result={proposal.before}/></p>}
     <p className="control-target">目标动作：<strong>{action}</strong></p>
    </div>
    {proposal.before && <p>观察时间：{observedTime(proposal.before.observed_at)}</p>}
    <p>有效期至：{observedTime(proposal.expires_at)}</p>
    <StatusBadge tone={tone} label={proposal.status === 'pending' && expired ? statusText.expired : statusText[proposal.status]} live/>
    {proposal.after && <p>核对状态：{deviceStateText(proposal.after)}<StateUnit result={proposal.after}/> · 观察时间：{observedTime(proposal.after.observed_at)}</p>}
    {['succeeded', 'unknown'].includes(proposal.status) && <Alert tone="warning">状态回读不能替代现场核验，请确认实际负载。</Alert>}
    {refreshError && <Alert tone="warning" live="assertive">{refreshError}</Alert>}
    {proposal.status === 'executing' && onRefresh && <Button variant="secondary" disabled={busy || refreshing} onClick={() => void invoke(onRefresh)}>刷新状态</Button>}
    {error && <Alert tone="danger" live="assertive">{error}</Alert>}
    {proposal.status === 'pending' && canControl && <div className="control-card-actions"><Button disabled={busy || expired} aria-label={`确认${action}${proposal.name}`} onClick={() => void invoke(onConfirm, true)}>确认{action}</Button><Button variant="secondary" disabled={busy || expired} onClick={() => void invoke(onCancel)}>取消提议</Button></div>}
    {proposal.status === 'unknown' && <div className="control-card-actions"><Button variant="secondary" disabled={busy} onClick={() => void invoke(onReconcile)}>核对状态</Button></div>}
    {proposal.status === 'pending' && !canControl && <p>当前账号没有设备控制权限。</p>}
    <Disclosure title="技术详情"><KeyValueList items={[{ id: 'entity', label: '实体', value: proposal.entity_id }, { id: 'before', label: '原始提议状态', value: proposal.before?.state ?? '暂无状态' }, { id: 'after', label: '原始核对状态', value: proposal.after?.state ?? '暂无状态' }]}/></Disclosure>
  </Card></section>
}
