import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import type { KnowledgeAnswer, KnowledgeIngest, VaultStatus } from '../api'
import { useConsoleAction } from '../consoleActions'
import { useFamilyQuery } from '../familyQuery'
import { KnowledgeBrowser } from '../knowledgeViews'
import SafeMarkdown from '../chat/SafeMarkdown'
import { errorText } from '../api'
import { Alert, Button, Card, Field, Input, LoadingState, PageFrame, PageHeader, Stack, Textarea, fieldDescriptionIds } from '../ui'
import '../styles/knowledge.css'

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

  return <PageFrame><Link className="jx-action-link jx-button-ghost" to="/app/account">返回我的</Link><PageHeader title="管理员私人知识" description="固定使用 personal，仅管理员可读。问答只保留在当前页面，不进入普通对话历史。"/>
    {status.error ? <Alert tone="danger" live="assertive">{errorText(status.error)}</Alert> : status.data ? <p className="jx-muted">personal · {status.data.page_count} 份资料 · {status.data.total_bytes} 字节</p> : <LoadingState label="正在读取私人库概况…"/>}
    <KnowledgeBrowser personal/>
    {validation && <Alert tone="danger" live="assertive">{validation}</Alert>}
    <Card heading="私人知识单次问答"><Stack><form onSubmit={ask}><Stack><Field id="private-query" label="私人问题" hint="1 至 4000 个字符，仅保留在当前页面。" required><Textarea id="private-query" rows={4} value={query} onChange={(event) => setQuery(event.target.value)} required disabled={questionAction.pending} aria-describedby={fieldDescriptionIds('private-query', true, false)}/></Field><Button type="submit" busy={questionAction.pending}>{questionAction.pending ? '正在问答…' : '单次问答'}</Button></Stack></form>{questionAction.error && <Alert tone="danger" live="assertive">{questionAction.error}</Alert>}{answer && <section aria-label="本次私人回答"><div className="jx-knowledge-markdown"><SafeMarkdown>{answer.answer}</SafeMarkdown></div><h3>本次来源</h3>{answer.sources.length ? <ul className="jx-knowledge-sources">{answer.sources.map((source) => <li key={source.path}><Link to={`/app/admin/knowledge?path=${encodeURIComponent(source.path)}`}>{source.title}</Link></li>)}</ul> : <p>本次没有返回可展示来源。</p>}</section>}</Stack></Card>
    <Card heading="文本导入" description="目标库：personal。导入成功不等于公开发布，不会自动分享给家人。"><Stack><form onSubmit={ingest}><Stack><Field id="import-title" label="来源标题" hint="最多 200 个字符。"><Input id="import-title" value={title} onChange={(event) => setTitle(event.target.value)} disabled={ingestAction.pending} aria-describedby={fieldDescriptionIds('import-title',true,false)}/></Field><Field id="import-content" label="导入文本" hint="1 至 100000 个字符。" required><Textarea id="import-content" rows={7} value={content} onChange={(event) => setContent(event.target.value)} required disabled={ingestAction.pending} aria-describedby={fieldDescriptionIds('import-content',true,false)}/></Field><Button type="submit" busy={ingestAction.pending}>{ingestAction.pending ? '正在导入…' : '导入到 personal'}</Button></Stack></form>{ingestAction.error && <Alert tone="danger" live="assertive">{ingestAction.error}</Alert>}{imported && <p role="status">已写入 personal：<Link to={`/app/admin/knowledge?path=${encodeURIComponent(imported.path)}`}>{imported.title}</Link>。尚未公开发布。</p>}</Stack></Card>
  </PageFrame>
}
