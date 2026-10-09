import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError, consoleApi, errorText, type ConsoleSession } from '../api'
import { useAuth } from '../auth'
import { Icon } from '../icons'
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
  return <main className="home-page">
    <section className="welcome"><h1>你好，{auth.identity?.user.display_name}</h1><p>看看家庭状态，也可以继续自己的对话。</p></section>
    <HomeFamilySummary/>
    <section className="home-entry-grid" aria-label="主要入口">{canChat && <button className="hero-card" onClick={() => start()}><Icon name="plus" size={24}/><span><strong>开启新对话</strong><small>提问、记录或一起理清想法</small></span><Icon name="arrow"/></button>}{canKnowledge && <Link className="hero-card" to="/app/knowledge"><Icon name="book" size={24}/><span><strong>公开知识</strong><small>查找现有网页聊天可使用的资料</small></span><Icon name="arrow"/></Link>}{canSuggestions && <Link className="hero-card" to="/app/automations"><Icon name="workflow" size={24}/><span><strong>自动化结果</strong><small>{admin ? '审核建议并明确共享确认结果' : '查看管理员明确共享的结果'}</small></span><Icon name="arrow"/></Link>}</section>
    {canSuggestions && <section className="panel home-automation-summary"><h2>{admin ? '建议待处理摘要' : '已共享自动化结果'}</h2>{suggestions.error ? <p role="alert">自动化结果暂时无法读取，请从结果页面核对。</p> : !suggestions.data ? <p role="status">正在读取结果摘要…</p> : <p>{admin ? <>{suggestions.data.suggestions.filter((item) => item.status === 'pending').length} 条待审核 · {suggestions.data.suggestions.filter((item) => item.status === 'applying' || item.status === 'failed').length} 条待核对</> : <>{suggestions.data.count} 条已共享确认结果</>}</p>}<Link className="text-link" to="/app/automations">{admin ? '查看并审核建议' : '查看共享结果'}<Icon name="arrow"/></Link></section>}
    <HomeCollectionSummary/>
    <section className="recent-section"><div className="section-heading"><h2>最近对话</h2>{sessions && sessions.length > 0 && <button className="text-link" onClick={() => start()}>查看对话<Icon name="arrow"/></button>}</div>
      {!canRead ? <div className="empty-card"><p>当前服务未提供历史记录。</p></div> : error ? <div className="empty-card error-state" role="alert"><p>{error}</p><button className="button button-secondary" onClick={() => setAttempt(attempt + 1)}>重新加载</button></div> : sessions === null ? <div className="empty-card" role="status"><span className="loader"/>正在读取最近对话…</div> : sessions.length === 0 ? <div className="empty-card"><Icon name="chat" size={24}/><strong>还没有对话记录</strong><p>你的第一段对话，会从这里开始。</p></div> : <div className="session-list">{sessions.slice(0, 5).map((session) => <button className="session-row" key={session.id} onClick={() => navigate(`/app/chat?sid=${encodeURIComponent(session.id)}`)}><Icon name="chat"/><span className="session-main"><strong>{session.preview || '未命名对话'}</strong><small>{session.message_count} 条消息</small></span><time>{timeLabel(session.last_active_at)}</time><Icon name="chevron"/></button>)}</div>}
    </section>
  </main>
}
