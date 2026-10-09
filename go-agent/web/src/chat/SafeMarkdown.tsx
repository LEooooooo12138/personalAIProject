import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function safeHref(href: string | undefined): string | undefined {
  if (!href) return undefined
  try {
    const url = new URL(href, window.location.origin)
    if (url.protocol === 'http:' || url.protocol === 'https:' || url.protocol === 'mailto:') return href
  } catch { /* Invalid destinations become plain text. */ }
  return undefined
}

export default function SafeMarkdown({ children }: { children: string }) {
  return <ReactMarkdown skipHtml remarkPlugins={[remarkGfm]} components={{
    img: ({ alt }) => <span>{alt ?? ''}</span>,
    a: ({ href, children: text }) => {
      const safe = safeHref(href)
      return safe ? <a href={safe} target="_blank" rel="noopener noreferrer">{text}</a> : <span>{text}</span>
    },
  }}>{children}</ReactMarkdown>
}
