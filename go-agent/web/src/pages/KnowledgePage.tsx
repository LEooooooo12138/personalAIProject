import { KnowledgeBrowser } from '../knowledgeViews'
import { PageFrame, PageHeader } from '../ui'
export default function KnowledgePage() {
 return <PageFrame><PageHeader title="公开知识" description="这是现有网页聊天也可使用的公开资料，不是仅家人可见的私人库。"/><KnowledgeBrowser/></PageFrame>
}
