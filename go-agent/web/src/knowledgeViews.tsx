import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import type { KnowledgePageView, KnowledgeSearch } from './api'
import { useFamilyQuery } from './familyQuery'
import { errorText } from './api'
import SafeMarkdown from './chat/SafeMarkdown'
import { Icon } from './icons'

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
  if (path) return <section className="knowledge-detail"><Link className="return-link" to={`${pageRoot}${query ? `?q=${encodeURIComponent(query)}` : ''}`}><Icon name="back"/>{personal ? '返回私人知识' : '返回公开知识'}</Link>
    {page.loading && !page.data ? <p role="status">正在读取正文…</p> : page.error ? <div className="state-panel danger" role="alert"><h2>未找到或暂时无法读取这份资料</h2><p>{errorText(page.error)}</p><button className="button button-secondary" onClick={page.reload}>重新读取正文</button></div> : page.data ? <><h2>{page.data.title}</h2><p className="relative-source">{page.data.path}</p><div className="knowledge-markdown"><SafeMarkdown>{page.data.body}</SafeMarkdown></div></> : null}
  </section>
  return <section className="knowledge-browser"><form className="search-form" onSubmit={search}><label htmlFor={personal ? 'private-search' : 'public-search'}>{personal ? '搜索私人知识' : '搜索公开知识'}</label><div className="search-controls"><input id={personal ? 'private-search' : 'public-search'} type="search" value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="输入想查找的内容"/><button className="button button-primary">{personal ? '检索私人知识' : '搜索知识'}</button></div></form>
    {validation && <p className="form-error" role="alert">{validation}</p>}
    {result.error ? <div className="state-panel danger" role="alert"><p>{errorText(result.error)}</p><button className="button button-secondary" onClick={result.reload}>重新读取搜索结果</button></div> : result.loading ? <p role="status">正在检索资料…</p> : result.data ? result.data.results.length === 0 ? <p className="empty-card">{personal ? '没有找到可读取的私人资料。' : '没有找到可读取的公开资料。'}</p> : <><p role="status">找到 {result.data.count} 份可读取资料</p><ul className="knowledge-results">{result.data.results.map((item) => <li key={item.path}><Link to={`${pageRoot}?q=${encodeURIComponent(query)}&path=${encodeURIComponent(item.path)}`}><Icon name="book"/><div><h2>{item.title}</h2><p>{item.snippet}</p><small>{item.path}</small></div><Icon name="arrow"/></Link></li>)}</ul></> : <p className="panel-description">输入关键词后查找资料，搜索失败会单独提示。</p>}
  </section>
}