import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { Metric } from './Metric'
import { ProgressSummary } from './ProgressSummary'
import { StatusBadge } from './StatusBadge'
import { Alert } from './Alert'
import { StepSequence } from './StepSequence'
it('distinguishes missing evidence from an actual zero',()=>{render(<><Metric label="Missing" value={null}/><Metric label="Count" value={0}/></>);expect(screen.getByText('暂无数据')).toBeInTheDocument();expect(screen.getByText('0')).toBeInTheDocument()})
it.each([[0,0],[NaN,10],[-1,10],[11,10],[1,Infinity],[.5,10]])('does not invent valid progress for %s/%s',(completed,total)=>{render(<ProgressSummary label="Batch" completed={completed} total={total} unit="项"/>);expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();expect(screen.getByText('进度暂无法确认')).toBeInTheDocument()})
it('renders honest zero progress with an accessible label',()=>{render(<ProgressSummary label="Batch" completed={0} total={4} unit="项"/>);expect(screen.getByRole('progressbar',{name:'Batch'})).toHaveAttribute('value','0')})
it('announces changes only when requested and describes every stage',()=>{render(<><StatusBadge tone="success" label="Verified"/><Alert tone="danger">Static</Alert><Alert tone="danger" live="assertive">Failed</Alert><StepSequence label="Stages" steps={['waiting','active','complete','failed','unknown'].map(state=>({id:state,label:state,state:state as 'waiting'}))}/></>);expect(screen.queryByRole('status')).not.toBeInTheDocument();expect(screen.getByRole('alert')).toHaveTextContent('Failed');for(const label of ['等待','进行中','已完成','失败','待确认']) expect(screen.getByText(label)).toBeInTheDocument()})
