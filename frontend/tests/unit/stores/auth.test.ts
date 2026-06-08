import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore, type AuthUser } from '~/stores/auth'

const adminUser: AuthUser = { id: 'u1', username: 'admin', role: 'admin', display_name: 'Admin' }
const viewerUser: AuthUser = { id: 'u2', username: 'viewer', role: 'viewer' }

describe('useAuthStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('starts unauthenticated', () => {
    const store = useAuthStore()
    expect(store.isAuthenticated).toBe(false)
    expect(store.token).toBeNull()
    expect(store.user).toBeNull()
    expect(store.role).toBe('anonymous')
    expect(store.isAdmin).toBe(false)
  })

  it('setAuth stores JWT + user; isAdmin true for admin role', () => {
    const store = useAuthStore()
    store.setAuth('jwt.abc.xyz', adminUser)
    expect(store.isAuthenticated).toBe(true)
    expect(store.token).toBe('jwt.abc.xyz')
    expect(store.user).toEqual(adminUser)
    expect(store.role).toBe('admin')
    expect(store.username).toBe('admin')
    expect(store.isAdmin).toBe(true)
  })

  it('setAuth with viewer role marks not admin', () => {
    const store = useAuthStore()
    store.setAuth('jwt.v', viewerUser)
    expect(store.isAuthenticated).toBe(true)
    expect(store.role).toBe('viewer')
    expect(store.isAdmin).toBe(false)
  })

  it('logout clears token and user', () => {
    const store = useAuthStore()
    store.setAuth('jwt', adminUser)
    store.logout()
    expect(store.isAuthenticated).toBe(false)
    expect(store.token).toBeNull()
    expect(store.user).toBeNull()
    expect(store.role).toBe('anonymous')
    expect(store.isAdmin).toBe(false)
  })

  it('setToken (legacy compat) still works with role parameter', () => {
    const store = useAuthStore()
    store.setToken('legacy-token', 'admin')
    expect(store.isAuthenticated).toBe(true)
    expect(store.token).toBe('legacy-token')
    expect(store.isAdmin).toBe(true)
  })
})
