import { render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { PreviewNotice } from './PreviewNotice'

afterEach(() => document.head.querySelectorAll('meta[name="console-preview"]').forEach(node => node.remove()))

it('shows the service supplied mock label without requesting an API', () => {
  const fetcher = vi.spyOn(globalThis, 'fetch')
  const meta = document.createElement('meta')
  meta.name = 'console-preview'
  meta.content = 'mock'
  document.head.append(meta)
  render(<PreviewNotice />)
  expect(screen.getByText('模拟预览 · 当前设备和自动化均为测试数据')).toBeVisible()
  expect(fetcher).not.toHaveBeenCalled()
})

it.each([undefined, 'production', 'true'])('does not infer mock mode without the exact service marker %s', content => {
  if (content !== undefined) {
    const meta = document.createElement('meta')
    meta.name = 'console-preview'
    meta.content = content
    document.head.append(meta)
  }
  render(<PreviewNotice />)
  expect(screen.queryByText('模拟预览 · 当前设备和自动化均为测试数据')).not.toBeInTheDocument()
})
