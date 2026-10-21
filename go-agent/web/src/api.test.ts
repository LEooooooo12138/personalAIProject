import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, consoleApi, errorText } from './api'

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

describe('safe diagnostic errors', () => {
  it.each([
    ['inference_auth_failed', /密钥.*(无效|认证)/],
    ['inference_forbidden', /权限/],
    ['inference_quota_exceeded', /额度|余额/],
    ['inference_rate_limited', /频繁|限流/],
    ['inference_timeout', /超时/],
    ['inference_model_unavailable', /模型/],
    ['control_unavailable', /设备/],
    ['unsupported_rule', /规则/],
    ['speech_auth_failed', /语音.*(密钥|认证)/],
  ])('classifies %s without a generic failure', (code, expected) => {
    expect(new ApiError(503, code).message).toMatch(expected)
  })
  it.each([[401, /登录/], [403, /权限/], [429, /频繁/], [502, /上游/], [504, /超时/], [503, /服务.*不可用/]])('uses HTTP %i when the error code is unknown', (status, expected) => {
    const error = new ApiError(status, 'new_failure')
    expect(error.message).toMatch(expected)
    expect(error.message).toContain(`HTTP ${status}`)
    expect(error.message).toContain('new_failure')
  })
  it('never displays arbitrary error code or request-id payloads as diagnostic text', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ error: { code: 'secret token=abc', message: 'secret body', request_id: '<secret token=abc>' } }, { status: 502 })))
    try { await consoleApi.sessions() } catch (error) {
      expect(errorText(error)).not.toMatch(/secret|token=abc/)
      expect(errorText(error)).toContain('HTTP 502')
    }
    expect(new ApiError(500, 'constructor').message).toContain('HTTP 500')
  })
})

it('identifies an unknown server rejection without claiming the server never responded', () => {
  const error = new ApiError(0, 'new_chat_failure')
  expect(error.message).toContain('new_chat_failure')
  expect(error.message).not.toMatch(/未收到|网络连接失败/)
})

it.each(['inference', 'speech'])('explains %s content filtering and ambiguous quota/rate limits', (service) => {
  expect(new ApiError(421, `${service}_content_filtered`).message).toMatch(/内容.*(安全|审核)/)
  expect(new ApiError(429, `${service}_rate_limited`).message).toMatch(/额度|套餐/)
  expect(new ApiError(403, `${service}_forbidden`).message).toContain('地区')
})
it('classifies malformed successful JSON as an invalid response without leaking its contents', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('<html>private proxy error</html>', { status: 200 })))
  const error = await consoleApi.sessions().catch((cause: unknown) => cause)
  expect(error).toBeInstanceOf(ApiError)
  expect(error).toMatchObject({ status: 502, code: 'invalid_response' })
  expect(errorText(error)).toMatch(/响应.*(格式|异常)/)
  expect(errorText(error)).not.toMatch(/网络连接失败|private proxy/)
})

it('timeout feedback preserves unknown write semantics', () => {
  const timeout = new ApiError(0, 'request_timeout')
  expect(errorText(timeout)).toContain('已提交的操作仍可能完成')
  expect(errorText(timeout)).toContain('先核对结果')
  expect(errorText(timeout)).toContain('不要重复提交')
})
