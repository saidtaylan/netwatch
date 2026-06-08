import { defineStore } from 'pinia'

export interface AuthUser {
  id: string
  username: string
  role: 'admin' | 'operator' | 'viewer'
  display_name?: string
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: null as string | null,
    user: null as AuthUser | null,
  }),
  getters: {
    isAuthenticated: (state) => !!state.token,
    isAdmin: (state) => state.user?.role === 'admin',
    role: (state) => state.user?.role ?? 'anonymous',
    username: (state) => state.user?.username ?? 'anonymous',
  },
  actions: {
    setAuth(token: string, user: AuthUser) {
      this.token = token
      this.user = user
    },
    // Legacy compat — still used by some existing code
    setToken(token: string, role: 'admin' | 'anonymous' = 'anonymous') {
      this.token = token
      this.user = { id: '', username: role, role: role === 'admin' ? 'admin' : 'viewer' }
    },
    logout() {
      this.token = null
      this.user = null
    },
  },
  persist: true,
})
