import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import type { KnowledgePageView, KnowledgeSearch } from './api'
import { useFamilyQuery } from './familyQuery'
import { errorText } from './api'
import SafeMarkdown from './chat/SafeMarkdown'
import { Alert, Button, EmptyState, Field, Icon, Input, ListRow, LoadingState, Section, Stack, fieldDescriptionIds } from './ui'
import './styles/knowledge.css'

export function KnowledgeBrowser({ personal = false }: { personal?: boolean }) {
  const [params, setParams] = useSearchParams()
  const query = params.get('q') ?? ''
  const path = params.get('path') ?? ''
  const [draft, setDraft] = useState(query)
  const [validation, setValidation] = useState('')
  useEffect(() => setDraft(query), [query])
  const root = personal ? '/admin/vault' : '/knowledge'
  const pageRoot = personal ? '/app/admin/knowledge' : '/app/knowledge'
  const result = useFamilyQuery<KnowledgeSearch>(`${root}/search?q=${encodeURIComponent(query)}`, Boolean(query) && !path)
  const page = useFamilyQuery<KnowledgePageView>(`${root}/page?path=${encodeURIComponent(path)}`, Boolean(path))
  function search(event: FormEvent) {
    event.preventDefault()
    const value = draft.trim()
    if (!value || [...value].length > 200) { setValidation('请输入 1 至 200 个字符。'); return }
    setValidation(''); if (value === query) result.reload(); else setParams({ q: value })
  }

  const fieldId = personal ? 'private-search' : 'public-search'
  if (path) return <Section><Link className="jx-action-link jx-button-ghost" to={`${pageRoot}${query ? `?q=${encodeURIComponent(query)}` : ''}`}><Icon name="back"/>{personal ? '返回私人知识' : '返回公开知识'}</Link>
    {page.loading && !page.data ? <LoadingState label="正在读取正文…"/> : page.error ? <Alert tone="danger" live="assertive" title="未找到或暂时无法读取这份资料" actions={<Button variant="secondary" onClick={page.reload}>重新读取正文</Button>}>{errorText(page.error)}</Alert> : page.data ? <article className="jx-knowledge-reading"><h2>{page.data.title}</h2><p className="jx-knowledge-source">{page.data.path}</p><div className="jx-knowledge-markdown"><SafeMarkdown>{page.data.body}</SafeMarkdown></div></article> : null}
  </Section>
  return <Section><Stack><form onSubmit={search}><div className="jx-knowledge-search"><Field id={fieldId} label={personal ? '搜索私人知识' : '搜索公开知识'} hint="输入 1 至 200 个字符。" error={validation || undefined}><Input id={fieldId} type="search" value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="输入想查找的内容" invalid={Boolean(validation)} aria-describedby={fieldDescriptionIds(fieldId, true, Boolean(validation))}/></Field><Button type="submit">{personal ? '检索私人知识' : '搜索知识'}</Button></div></form>
    {validation && <Alert tone="danger" live="assertive">{validation}</Alert>}
    {result.error ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={result.reload}>重新读取搜索结果</Button>}>{errorText(result.error)}</Alert> : result.loading ? <LoadingState label="正在检索资料…"/> : result.data ? result.data.results.length === 0 ? <EmptyState title={personal ? '没有找到可读取的私人资料。' : '没有找到可读取的公开资料。'} icon="book"/> : <><p role="status">找到 {result.data.count} 份可读取资料</p><ul className="jx-plain-list jx-knowledge-results">{result.data.results.map((item) => <li key={item.path}><Link to={`${pageRoot}?q=${encodeURIComponent(query)}&path=${encodeURIComponent(item.path)}`}><ListRow leading={<Icon name="book"/>} title={<h2>{item.title}</h2>} description={item.snippet} meta={<span className="jx-knowledge-source">{item.path}</span>} trailing={<Icon name="arrow"/>}/></Link></li>)}</ul></> : <p className="jx-muted">输入关键词后查找资料，搜索失败会单独提示。</p>}
  </Stack></Section>
}
