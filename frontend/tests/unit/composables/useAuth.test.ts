/**
 * useAuth tests — single-admin-token flow.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '~/stores/auth'
import { useNodesStore } from '~/stores/nodes'

describe('useAuth.checkToken', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('returns role from /auth/whoami', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({ role: 'admin' })
    const { useAuth } = await import('~/composables/useAuth')
    const { checkToken } = useAuth()
    const role = await checkToken('http://localhost:10240', 'admin-token')
    expect(role).toBe('admin')
  })

  it('returns anonymous when no token configured', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({ role: 'anonymous' })
    const { useAuth } = await import('~/composables/useAuth')
    const { checkToken } = useAuth()
    const role = await checkToken('http://localhost:10240', '')
    expect(role).toBe('anonymous')
  })

  it('omits Authorization header when token is empty', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({ role: 'anonymous' })
    const { useAuth } = await import('~/composables/useAuth')
    const { checkToken } = useAuth()
    await checkToken('http://localhost:10240', '')
    const callArgs = vi.mocked($fetch).mock.calls[0]
    expect(callArgs[1]?.headers).toEqual({})
  })

  it('sends Bearer header when token provided', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({ role: 'admin' })
    const { useAuth } = await import('~/composables/useAuth')
    const { checkToken } = useAuth()
    await checkToken('http://localhost:10240', 'secret')
    const callArgs = vi.mocked($fetch).mock.calls[0]
    expect((callArgs[1]?.headers as any)?.Authorization).toBe('Bearer secret')
  })
})

describe('useAuth.login', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('stores token + role after successful verification', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://localhost:10240')
    nodes.setActive('http://localhost:10240')
    nodes.markHealthy('http://localhost:10240')

    vi.mocked($fetch).mockResolvedValueOnce({ role: 'admin' })

    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await login('admin-token')

    const auth = useAuthStore()
    expect(auth.token).toBe('admin-token')
    expect(auth.role).toBe('admin')
    expect(auth.isAuthenticated).toBe(true)
  })

  it('throws when no backend node available', async () => {
    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await expect(login('any')).rejects.toThrow(/No backend node reachable/)
  })

  it('propagates checkToken failure (does NOT mark as auth)', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://localhost:10240')
    nodes.setActive('http://localhost:10240')
    nodes.markHealthy('http://localhost:10240')

    vi.mocked($fetch).mockRejectedValueOnce(new Error('401'))

    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await expect(login('wrong-token')).rejects.toThrow()

    const auth = useAuthStore()
    expect(auth.isAuthenticated).toBe(false)
  })
})

describe('useAuth.logout', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('clears token + role (navigation tested in e2e)', async () => {
    const auth = useAuthStore()
    auth.setToken('some-token', 'admin')

    const { useAuth } = await import('~/composables/useAuth')
    const { logout } = useAuth()
    // logout returns a Promise (navigateTo). We don't await it because
    // navigateTo behavior in unit-test environment depends on the Nuxt
    // test runtime — e2e tests verify the redirect.
    logout()

    expect(auth.token).toBeNull()
    expect(auth.role).toBe('anonymous')
  })
})
