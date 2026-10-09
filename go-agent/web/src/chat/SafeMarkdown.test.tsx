import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import SafeMarkdown from './SafeMarkdown'

describe('safe Markdown', () => {
  it('never renders HTML, remote images, or script links', () => {
    const { container } = render(<SafeMarkdown>{'<script>alert(1)</script>\n\n![tracking](https://evil.example/pixel) [bad](javascript:alert(1)) [good](https://example.org)'}</SafeMarkdown>)
    expect(container.querySelector('script, img')).toBeNull()
    expect(screen.getByText('bad').closest('a')).toBeNull()
    expect(screen.getByRole('link', { name: 'good' })).toHaveAttribute('rel', 'noopener noreferrer')
  })
})
