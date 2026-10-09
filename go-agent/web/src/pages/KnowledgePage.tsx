import { KnowledgeBrowser } from '../knowledgeViews'
export default function KnowledgePage() {
  return <main className="content-page knowledge-page"><div className="page-intro"><h1>公开知识</h1><p>这是现有网页聊天也可使用的公开资料，不是仅家人可见的私人库。</p></div><KnowledgeBrowser/></main>
}