import { defineStore } from 'pinia'

export interface AuthUser {
  id: string
  username: string
  role: 'admin' | 'operator' | 'viewer'
  display_name?: string
}

// useAuthStore holds the JWT and the logged-in user. Persisted to localStorage
// so a refresh keeps the session (useApi sends the token as a Bearer header).
export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: null as string | null,
    user: null as AuthUser | null,
  }),
  getters: {
    /** True when a JWT is present. */
    isAuthenticated: (state) => !!state.token,
    /** True when the logged-in user has the admin role. */
    isAdmin: (state) => state.user?.role === 'admin',
    /** The user's role, or "anonymous" when logged out. */
    role: (state) => state.user?.role ?? 'anonymous',
    /** The user's username, or "anonymous" when logged out. */
    username: (state) => state.user?.username ?? 'anonymous',
  },
  actions: {
    /** Stores the JWT and user after a successful login/setup. */
    setAuth(token: string, user: AuthUser) {
      this.token = token
      this.user = user
    },
    /** Legacy single-token setter kept for backward compatibility. */
    setToken(token: string, role: 'admin' | 'anonymous' = 'anonymous') {
      this.token = token
      this.user = { id: '', username: role, role: role === 'admin' ? 'admin' : 'viewer' }
    },
    /** Clears the token and user (sign out). */
    logout() {
      this.token = null
      this.user = null
    },
  },
  persist: true,
})
