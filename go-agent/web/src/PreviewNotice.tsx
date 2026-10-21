import { Alert } from './ui'

// Only the disposable fixture adds this marker to its own copy of the HTML.
export function PreviewNotice() {
  if (document.head.querySelector('meta[name="console-preview"]')?.getAttribute('content') !== 'mock') return null
  return <Alert tone="warning">模拟预览 · 当前设备和自动化均为测试数据</Alert>
}
