import { useId } from 'react'
export function ProgressSummary({ label, completed, total, unit }: { label: string; completed: number; total: number; unit: string }) {
 const id = useId(); const valid = Number.isSafeInteger(total) && Number.isSafeInteger(completed) && total > 0 && completed >= 0 && completed <= total
 return <div data-jx-ui="" className="jx-progress"><label htmlFor={valid ? id : undefined}>{label}</label>{valid ? <><progress id={id} value={completed} max={total}/><span>{completed} / {total} {unit}</span></> : <span className="jx-muted">进度暂无法确认</span>}</div>
}
