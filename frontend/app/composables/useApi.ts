/**
 * useApi — $fetch wrapper with:
 *  - auto base URL from active node
 *  - Authorization: Bearer header injection
 *  - 401 → auto logout
 *  - Network error → failover + single retry
 */
import type { FetchOptions } from 'ofetch'

export const useApi = () => {
  const auth  = useAuthStore()
  const nodes = useNodesStore()
  const { ensureActive, selectActiveNode } = useNodeConnection()

  async function call<T>(path: string, opts: FetchOptions<'json'> = {}): Promise<T> {
    const baseUrl = await ensureActive()
    if (!baseUrl) throw new Error('No backend node available')

    const headers: Record<string, string> = {
      ...(opts.headers as Record<string, string> ?? {}),
    }
    if (auth.token) {
      headers['Authorization'] = `Bearer ${auth.token}`
    }

    try {
      return await $fetch<T>(`${baseUrl}${path}`, {
        ...opts,
        headers,
        timeout: 10000,
      })
    } catch (err: any) {
      // 401 → logout
      if (err?.response?.status === 401) {
        auth.logout()
        await navigateTo('/login')
        throw err
      }
      // Network error → try failover once
      if (!err?.response) {
        nodes.markUnhealthy(baseUrl)
        const fallback = await selectActiveNode()
        if (fallback && fallback !== baseUrl) {
          return $fetch<T>(`${fallback}${path}`, { ...opts, headers, timeout: 10000 })
        }
      }
      throw err
    }
  }

  const get  = <T>(path: string, opts?: FetchOptions<'json'>)        => call<T>(path, { ...opts, method: 'GET' })
  const post = <T>(path: string, body?: unknown, opts?: FetchOptions<'json'>) => call<T>(path, { ...opts, method: 'POST', body })
  const put  = <T>(path: string, body?: unknown, opts?: FetchOptions<'json'>) => call<T>(path, { ...opts, method: 'PUT',  body })
  const del  = <T>(path: string, opts?: FetchOptions<'json'>)        => call<T>(path, { ...opts, method: 'DELETE' })

  return { get, post, put, del }
}
