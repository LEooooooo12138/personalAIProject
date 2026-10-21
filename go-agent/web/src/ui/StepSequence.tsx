export type StepItem = { id: string; label: string; state: 'waiting' | 'active' | 'complete' | 'failed' | 'unknown' }
const stateLabels = { waiting: '等待', active: '进行中', complete: '已完成', failed: '失败', unknown: '待确认' }
export function StepSequence({ label, steps }: { label: string; steps: readonly StepItem[] }) { return <ol data-jx-ui="" className="jx-steps" aria-label={label}>{steps.map(step => <li key={step.id} className={`jx-step-${step.state}`} aria-current={step.state === 'active' ? 'step' : undefined}><span>{step.label}</span><strong>{stateLabels[step.state]}</strong></li>)}</ol> }
