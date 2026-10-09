import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, consoleApi } from './api'

afterEach(() => vi.unstubAllGlobals())

describe('console API boundary', () => {
  it('uses same-origin cookies, CSRF for writes, and accepts an empty 204', async () => {
    const fetcher = vi.fn(async () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetcher)
    await expect(consoleApi.logout('csrf-123')).resolves.toBeUndefined()
    const [url, options] = fetcher.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/console/v1/auth/logout')
    expect(options.credentials).toBe('same-origin')
    expect(options.headers).toMatchObject({ 'X-CSRF-Token': 'csrf-123' })
  })

  it('forwards AbortSignal and never renders raw server error text', async () => {
    const controller = new AbortController()
    const fetcher = vi.fn(async (_url: string, _options?: RequestInit) => new Response(JSON.stringify({ error: {
      code: 'unavailable', message: '<script>secret</script>', request_id: 'req-42',
    } }), { status: 503, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetcher)
    await expect(consoleApi.sessions(controller.signal)).rejects.toMatchObject({
      name: 'ApiError', code: 'unavailable', requestId: 'req-42',
    } satisfies Partial<ApiError>)
    const options = fetcher.mock.calls[0][1] as RequestInit
    expect(options.signal).toBe(controller.signal)
    try { await consoleApi.sessions(controller.signal) } catch (error) {
      expect((error as ApiError).message).not.toContain('<script>')
    }
  })

  it('sends exact member DTOs for create, disable, and password reset', async () => {
    const fetcher = vi.fn(async (url: string, options: RequestInit) => {
      if (url.endsWith('reset-password')) return Response.json({ temporary_password: 'fresh-secret' })
      if (options.method === 'PATCH') return Response.json({ user: { id: 'm1', username: 'mei', display_name: '梅', role: 'member', disabled: true, must_change_password: false } })
      return Response.json({ user: { id: 'm1', username: 'mei', display_name: '梅', role: 'member', disabled: false, must_change_password: true }, temporary_password: 'temporary-secret' }, { status: 201 })
    })
    vi.stubGlobal('fetch', fetcher)
    await consoleApi.createMember({ username: 'mei', display_name: '梅' }, 'csrf')
    await consoleApi.updateMember('m1', { disabled: true }, 'csrf')
    await consoleApi.resetMemberPassword('m1', 'csrf')
    expect(fetcher.mock.calls.map(([url, options]) => [url, options.method, options.body])).toEqual([
      ['/api/console/v1/admin/members', 'POST', '{"username":"mei","display_name":"梅"}'],
      ['/api/console/v1/admin/members/m1', 'PATCH', '{"disabled":true}'],
      ['/api/console/v1/admin/members/m1/reset-password', 'POST', undefined],
    ])
  })
})
