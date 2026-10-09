import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import type { KnowledgeAnswer, KnowledgeIngest, VaultStatus } from '../api'
import { useConsoleAction } from '../consoleActions'
import { useFamilyQuery } from '../familyQuery'
import { KnowledgeBrowser } from '../knowledgeViews'
import SafeMarkdown from '../chat/SafeMarkdown'
import { errorText } from '../api'

export default function AdminKnowledgePage() {
  const status = useFamilyQuery<VaultStatus>('/admin/vault/status')
  const questionAction = useConsoleAction()
  const ingestAction = useConsoleAction()
  const [query, setQuery] = useState('')
  const [answer, setAnswer] = useState<KnowledgeAnswer | null>(null)
  const [content, setContent] = useState('')
  const [title, setTitle] = useState('')
  const [imported, setImported] = useState<KnowledgeIngest | null>(null)
  const [validation, setValidation] = useState('')
  async function ask(event: FormEvent) {
    event.preventDefault(); setValidation('')
    if (!query.trim() || [...query].length > 4000) { setValidation('私人问题需为 1 至 4000 个字符。'); return }
    setAnswer(null)
    const result = await questionAction.run<KnowledgeAnswer>('/admin/knowledge/query', { query })
    if (result) setAnswer(result)
  }
  async function ingest(event: FormEvent) {
    event.preventDefault(); setValidation('')
    if (!content.trim() || [...content].length > 100000 || [...title].length > 200) { setValidation('导入文本需为 1 至 100000 个字符，来源标题最多 200 个字符。'); return }
    setImported(null)
    const result = await ingestAction.run<KnowledgeIngest>('/admin/wiki/ingest', { content, source_title: title })
    if (result) { setImported(result); setContent(''); setTitle(''); status.reload() }
  }
  return <main className="content-page private-knowledge-page"><Link className="return-link" to="/app/account">返回我的</Link><div className="page-intro"><h1>管理员私人知识</h1><p>固定使用 personal，仅管理员可读。问答只保留在当前页面，不进入普通对话历史。</p></div>
    {status.error ? <p role="alert" className="form-error">{errorText(status.error)}</p> : status.data ? <p className="vault-summary">personal · {status.data.page_count} 份资料 · {status.data.total_bytes} 字节</p> : <p role="status">正在读取私人库概况…</p>}
    <KnowledgeBrowser personal/>
    {validation && <p role="alert" className="form-error">{validation}</p>}
    <section className="panel private-question"><h2>私人知识单次问答</h2><form onSubmit={ask}><label htmlFor="private-query">私人问题</label><textarea id="private-query" rows={4} value={query} onChange={(event) => setQuery(event.target.value)} required disabled={questionAction.pending}/><button className="button button-primary" disabled={questionAction.pending}>{questionAction.pending ? '正在问答…' : '单次问答'}</button></form>{questionAction.error && <p role="alert" className="form-error">{questionAction.error}</p>}{answer && <section aria-label="本次私人回答"><div className="knowledge-markdown"><SafeMarkdown>{answer.answer}</SafeMarkdown></div><h3>本次来源</h3>{answer.sources.length ? <ul>{answer.sources.map((source) => <li key={source.path}><Link to={`/app/admin/knowledge?path=${encodeURIComponent(source.path)}`}>{source.title}</Link></li>)}</ul> : <p>本次没有返回可展示来源。</p>}</section>}</section>
    <section className="panel private-import"><h2>文本导入</h2><p>目标库：personal。导入成功不等于公开发布，不会自动分享给家人。</p><form onSubmit={ingest}><label htmlFor="import-title">来源标题</label><input id="import-title" value={title} onChange={(event) => setTitle(event.target.value)} disabled={ingestAction.pending}/><label htmlFor="import-content">导入文本</label><textarea id="import-content" rows={7} value={content} onChange={(event) => setContent(event.target.value)} required disabled={ingestAction.pending}/><button className="button button-primary" disabled={ingestAction.pending}>{ingestAction.pending ? '正在导入…' : '导入到 personal'}</button></form>{ingestAction.error && <p role="alert" className="form-error">{ingestAction.error}</p>}{imported && <p role="status">已写入 personal：<Link to={`/app/admin/knowledge?path=${encodeURIComponent(imported.path)}`}>{imported.title}</Link>。尚未公开发布。</p>}</section>
  </main>
}