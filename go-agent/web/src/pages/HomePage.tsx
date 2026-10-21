import { Alert, Button, EmptyState, Grid, LoadingState, Metric, PageFrame, PageHeader, Section } from "../ui"
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError, consoleApi, errorText, type ConsoleSession } from '../api'
import { useAuth } from '../auth'
import { Icon } from '../ui'
import HomeFamilySummary from '../HomeFamilySummary'
import HomeCollectionSummary from '../HomeCollectionSummary'
import { Link } from 'react-router-dom'
import { useFamilyQuery } from '../familyQuery'
import type { SuggestionList } from '../api'

function timeLabel(value: string) {
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '' : new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(time)
}

export default function HomePage() {
  const auth = useAuth()
  const navigate = useNavigate()
  const [sessions, setSessions] = useState<ConsoleSession[] | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const canRead = auth.identity?.capabilities.includes('sessions:read') ?? false
  const canChat = auth.identity?.capabilities.includes('chat:use') ?? false
  const canSuggestions = auth.identity?.capabilities.includes('suggestions:read') ?? false
  const canKnowledge = auth.identity?.capabilities.includes('knowledge:read') ?? false
  const admin = auth.identity?.user.role === 'admin'
  const suggestions = useFamilyQuery<SuggestionList>('/suggestions', canSuggestions)
  useEffect(() => {
    if (!canRead) return
    let active = true
    const expected = auth.epoch
    const controller = new AbortController()
    const onAbort = () => controller.abort()
    auth.signal.addEventListener('abort', onAbort, { once: true })
    setSessions(null); setError('')
    void consoleApi.sessions(controller.signal).then((result) => {
      if (active && auth.isCurrent(expected)) setSessions(result.sessions)
    }).catch((cause) => {
      if (!active || !auth.isCurrent(expected)) return
      if (cause instanceof ApiError && cause.status === 401) auth.expire()
      else if (!(cause instanceof DOMException && cause.name === 'AbortError')) setError(errorText(cause))
    })
    return () => { active = false; controller.abort(); auth.signal.removeEventListener('abort', onAbort) }
  }, [canRead, auth.epoch, attempt])
  function start(draft = '') { navigate('/app/chat', { state: { draft } }) }
  return <PageFrame className="jx-home">
    <PageHeader title={'你好，' + (auth.identity?.user.display_name ?? '')} description="看看家庭状态，也可以继续自己的对话。"/>
    <section aria-label="主要入口"><Grid columns={canChat ? 1 : 2} gap={16}>
      {canChat && <Button size="comfortable" className="jx-home-primary" onClick={() => start()}><Icon name="chat" size={32}/><span><strong>开启新对话</strong><small>提问、记录或一起理清想法</small></span><Icon name="arrow"/></Button>}
      <Grid columns={2} gap={16}>{canKnowledge && <Link className="jx-entry" to="/app/knowledge"><Icon name="book"/><span>公开知识<small>查找现有网页聊天可使用的资料</small></span><Icon name="arrow"/></Link>}{canSuggestions && <Link className="jx-entry" to="/app/automations"><Icon name="workflow"/><span>自动化结果<small>{admin ? '审核建议并明确共享确认结果' : '查看管理员明确共享的结果'}</small></span><Icon name="arrow"/></Link>}</Grid>
    </Grid></section>
    {canSuggestions && <Section title={admin ? '建议待处理摘要' : '已共享自动化结果'} actions={<Link className="jx-action-link" to="/app/automations">{admin ? '查看并审核建议' : '查看共享结果'}<Icon name="arrow"/></Link>}>
      {suggestions.error ? <Alert tone="warning" live="assertive">自动化结果暂时无法读取，请从结果页面核对。</Alert> : !suggestions.data ? <LoadingState label="正在读取结果摘要…"/> : <Grid columns={2} gap={16}>{admin ? <><Metric label="待审核" value={suggestions.data.suggestions.filter((item) => item.status === 'pending').length} unit="条"/><Metric label="待核对" value={suggestions.data.suggestions.filter((item) => item.status === 'applying' || item.status === 'failed').length} unit="条"/></> : <Metric label="已共享确认结果" value={suggestions.data.count} unit="条"/>}</Grid>}
    </Section>}
    <HomeFamilySummary/>
    <HomeCollectionSummary/>
    <Section title="最近对话" actions={sessions && sessions.length > 0 ? <Button variant="ghost" onClick={() => start()}>查看对话<Icon name="arrow"/></Button> : undefined}>
      {!canRead ? <EmptyState title="当前服务未提供历史记录。"/> : error ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={() => setAttempt(attempt + 1)}>重新加载</Button>}>{error}</Alert> : sessions === null ? <LoadingState label="正在读取最近对话…"/> : sessions.length === 0 ? <EmptyState icon="chat" title="还没有对话记录" description="你的第一段对话，会从这里开始。"/> : <ul className="jx-plain-list">{sessions.slice(0, 5).map((session) => <li key={session.id}><Button variant="ghost" className="jx-session-row" onClick={() => navigate(`/app/chat?sid=${encodeURIComponent(session.id)}`)}><Icon name="chat"/><span className="jx-session-preview"><strong>{session.preview || '未命名对话'}</strong><small>{session.message_count} 条消息</small></span><time>{timeLabel(session.last_active_at)}</time><Icon name="chevron"/></Button></li>)}</ul>}
    </Section>
  </PageFrame>

}
