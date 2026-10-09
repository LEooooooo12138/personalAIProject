import { useEffect, useRef, useState } from 'react'
import { deviceStateText, observedTime, type ControlProposal, type DeviceResult } from './control-types'

type Props = {
  proposal: ControlProposal
  canControl: boolean
  error?: string
  onConfirm: (id: string) => Promise<void>
  onCancel: (id: string) => Promise<void>
  onReconcile: (id: string) => Promise<void>
}
const statusText = { pending: '等待确认', executing: '正在执行，无法撤回', succeeded: '已观察到目标状态', failed: '操作未完成，请重新提出需求', unknown: '结果尚未确认，请核对状态', cancelled: '提议已取消', expired: '提议已过期，请重新提出需求' }
export function DeviceResultCard({ result }: { result: DeviceResult }) {
  return <section className="control-card device-result" aria-label={`${result.name}设备状态`}><strong>{result.name}</strong><p>当前状态：{deviceStateText(result.state)}</p><p>观察时间：{observedTime(result.observed_at)}</p></section>
}
export default function ControlCard({ proposal, canControl, error, onConfirm, onCancel, onReconcile }: Props) {
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
  const expired = !Number.isFinite(expiry) || now >= expiry
  const action = proposal.action === 'turn_on' ? '打开' : '关闭'
  async function invoke(callback: (id: string) => Promise<void>, confirming = false) {
    if (running.current || (confirming && (!Number.isFinite(expiry) || Date.now() >= expiry))) return
    running.current = true; setBusy(true)
    try { await callback(proposal.id) } finally { running.current = false; if (mounted.current) setBusy(false) }
  }
  return <section className="control-card" aria-label={`${proposal.name}控制提议`} aria-busy={busy}>
    <header><strong>{proposal.name}</strong><span>{proposal.area_name || '未标注房间'}</span></header>
    <p>目标动作：<strong>{action}</strong></p>
    {proposal.before && <><p>当前状态：{deviceStateText(proposal.before.state)}</p><p>观察时间：{observedTime(proposal.before.observed_at)}</p></>}
    <p>有效期至：{observedTime(proposal.expires_at)}</p>
    <p role="status">{proposal.status === 'pending' && expired ? statusText.expired : statusText[proposal.status]}</p>
    {proposal.after && <p>核对状态：{deviceStateText(proposal.after.state)} · 观察时间：{observedTime(proposal.after.observed_at)}</p>}
    {['succeeded', 'unknown'].includes(proposal.status) && <small>状态回读不能替代现场核验，请确认实际负载。</small>}
    {error && <p className="chat-inline-error" role="alert">{error}</p>}
    {proposal.status === 'pending' && canControl && <div className="control-card-actions"><button className="button button-primary" type="button" disabled={busy || expired} aria-label={`确认${action}${proposal.name}`} onClick={() => void invoke(onConfirm, true)}>确认{action}</button><button className="button button-secondary" type="button" disabled={busy || expired} onClick={() => void invoke(onCancel)}>取消提议</button></div>}
    {proposal.status === 'unknown' && <div className="control-card-actions"><button className="button button-secondary" type="button" disabled={busy} onClick={() => void invoke(onReconcile)}>核对状态</button></div>}
    {proposal.status === 'pending' && !canControl && <small>当前账号没有设备控制权限。</small>}
  </section>
}
