import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '~/stores/auth'

describe('useAuthStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('starts unauthenticated', () => {
    const store = useAuthStore()
    expect(store.isAuthenticated).toBe(false)
    expect(store.token).toBeNull()
    expect(store.role).toBe('anonymous')
  })

  it('setToken marks as authenticated with role', () => {
    const store = useAuthStore()
    store.setToken('secret-token', 'admin')
    expect(store.isAuthenticated).toBe(true)
    expect(store.token).toBe('secret-token')
    expect(store.role).toBe('admin')
    expect(store.isAdmin).toBe(true)
  })

  it('setToken defaults role to anonymous when not specified', () => {
    const store = useAuthStore()
    store.setToken('some-token')
    expect(store.role).toBe('anonymous')
    expect(store.isAdmin).toBe(false)
  })

  it('logout clears token and role', () => {
    const store = useAuthStore()
    store.setToken('secret-token', 'admin')
    store.logout()
    expect(store.isAuthenticated).toBe(false)
    expect(store.token).toBeNull()
    expect(store.role).toBe('anonymous')
    expect(store.isAdmin).toBe(false)
  })
})
