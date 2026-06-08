/**
 * useAuth tests — JWT-based multi-user auth (B28/B31).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '~/stores/auth'
import { useNodesStore } from '~/stores/nodes'

const adminUser = { id: 'u1', username: 'admin', role: 'admin' as const, display_name: 'Admin' }

function seedActiveNode() {
  const nodes = useNodesStore()
  nodes.addNode('http://localhost:10240')
  nodes.setActive('http://localhost:10240')
  nodes.markHealthy('http://localhost:10240')
}

describe('useAuth.checkStatus', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('returns setup_completed flag from /auth/status', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({ setup_completed: false, user_count: 0 })
    const { useAuth } = await import('~/composables/useAuth')
    const { checkStatus } = useAuth()
    const status = await checkStatus('http://localhost:10240')
    expect(status.setup_completed).toBe(false)
    expect(status.user_count).toBe(0)
  })

  it('hits /auth/status on the provided baseUrl', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({ setup_completed: true, user_count: 1 })
    const { useAuth } = await import('~/composables/useAuth')
    const { checkStatus } = useAuth()
    await checkStatus('http://localhost:10240')
    expect(vi.mocked($fetch).mock.calls[0][0]).toBe('http://localhost:10240/auth/status')
  })
})

describe('useAuth.setup', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('POSTs setup payload and stores returned JWT + user', async () => {
    seedActiveNode()
    vi.mocked($fetch).mockResolvedValueOnce({ token: 'jwt.x', user: adminUser })

    const { useAuth } = await import('~/composables/useAuth')
    const { setup } = useAuth()
    const resp = await setup('setup-token-abc', 'admin', 'pw12345678', 'Admin')

    expect(resp.token).toBe('jwt.x')
    const auth = useAuthStore()
    expect(auth.token).toBe('jwt.x')
    expect(auth.user).toEqual(adminUser)
    expect(auth.isAdmin).toBe(true)

    const [url, opts] = vi.mocked($fetch).mock.calls[0]
    expect(url).toBe('http://localhost:10240/auth/setup')
    expect(opts?.method).toBe('POST')
    expect((opts?.body as any).setup_token).toBe('setup-token-abc')
    expect((opts?.body as any).username).toBe('admin')
    expect((opts?.body as any).password).toBe('pw12345678')
    expect((opts?.body as any).node_urls).toEqual(['http://localhost:10240'])
  })

  it('throws when no backend node available', async () => {
    const { useAuth } = await import('~/composables/useAuth')
    const { setup } = useAuth()
    await expect(setup('t', 'u', 'p')).rejects.toThrow(/No backend node reachable/)
  })

  it('propagates backend error and does NOT mark as authenticated', async () => {
    seedActiveNode()
    vi.mocked($fetch).mockRejectedValueOnce(new Error('400'))

    const { useAuth } = await import('~/composables/useAuth')
    const { setup } = useAuth()
    await expect(setup('bad', 'u', 'p')).rejects.toThrow()
    expect(useAuthStore().isAuthenticated).toBe(false)
  })
})

describe('useAuth.login', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('stores JWT + user on success', async () => {
    seedActiveNode()
    vi.mocked($fetch).mockResolvedValueOnce({ token: 'jwt.y', user: adminUser })

    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await login('admin', 'pw12345678')

    const auth = useAuthStore()
    expect(auth.token).toBe('jwt.y')
    expect(auth.user).toEqual(adminUser)
  })

  it('seeds cluster_nodes from login response', async () => {
    seedActiveNode()
    vi.mocked($fetch).mockResolvedValueOnce({
      token: 'jwt.z', user: adminUser,
      cluster_nodes: ['http://localhost:10240', 'http://peer:10240'],
    })

    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await login('admin', 'pw12345678')

    const nodes = useNodesStore()
    expect(nodes.configured.map(n => n.url)).toContain('http://peer:10240')
  })

  it('throws when no backend node available', async () => {
    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await expect(login('u', 'p')).rejects.toThrow(/No backend node reachable/)
  })

  it('propagates login failure (does NOT mark as auth)', async () => {
    seedActiveNode()
    vi.mocked($fetch).mockRejectedValueOnce(new Error('401'))

    const { useAuth } = await import('~/composables/useAuth')
    const { login } = useAuth()
    await expect(login('u', 'wrong')).rejects.toThrow()
    expect(useAuthStore().isAuthenticated).toBe(false)
  })
})

describe('useAuth.logout', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('clears token + user (navigation tested in e2e)', async () => {
    const auth = useAuthStore()
    auth.setAuth('jwt', adminUser)

    const { useAuth } = await import('~/composables/useAuth')
    const { logout } = useAuth()
    // navigateTo returns a Promise; not awaited (Nuxt runtime tested via e2e).
    logout()

    expect(auth.token).toBeNull()
    expect(auth.user).toBeNull()
  })
})
