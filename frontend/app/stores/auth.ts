import { defineStore } from 'pinia'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: null as string | null,
    role: 'anonymous' as 'admin' | 'anonymous',
  }),
  getters: {
    isAuthenticated: (state) => !!state.token,
    isAdmin: (state) => state.role === 'admin',
  },
  actions: {
    setToken(token: string, role: 'admin' | 'anonymous' = 'anonymous') {
      this.token = token
      this.role = role
    },
    logout() {
      this.token = null
      this.role = 'anonymous'
    },
  },
  persist: true,
})
