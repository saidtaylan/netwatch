/**
 * useApi — $fetch wrapper with:
 *  - auto base URL from active node
 *  - Authorization: Bearer header injection
 *  - 401 → auto logout
 *  - Network error → failover + single retry
 *  - null active node → short retry for pinia hydration (stores loaded from localStorage async)
 */
import type { FetchOptions } from 'ofetch'

/** Wait up to `maxMs` for `predicate` to return truthy, polling every `intervalMs`. */
async function waitFor(predicate: () => boolean, maxMs = 300, intervalMs = 50): Promise<boolean> {
  const deadline = Date.now() + maxMs
  while (Date.now() < deadline) {
    if (predicate()) return true
    await new Promise(r => setTimeout(r, intervalMs))
  }
  return predicate()
}

/** Returns the API helper bound to the active backend node (see file header). */
export const useApi = () => {
  const auth  = useAuthStore()
  const nodes = useNodesStore()
  const { ensureActive, selectActiveNode } = useNodeConnection()

  /**
   * call performs a typed request to `path` on the active node. It resolves the
   * base URL (waiting briefly for Pinia hydration if needed), injects the JWT,
   * and on a network error fails over to another node and retries once; a 401
   * logs the user out. Returns the parsed JSON of type T, or throws.
   */
  async function call<T>(path: string, opts: FetchOptions<'json'> = {}): Promise<T> {
    let baseUrl = await ensureActive()

    // Safety net for pinia hydration timing: if nodes.configured is empty (store
    // not yet hydrated from localStorage), wait briefly and retry.
    if (!baseUrl && nodes.configured.length === 0) {
      await waitFor(() => nodes.configured.length > 0)
      baseUrl = await ensureActive()
    }

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
      // 401 = missing / expired / invalid token → force logout
      // 403 = valid token but insufficient role → caller handles
      if (err?.response?.status === 401) {
        auth.logout()
        await navigateTo({ name: 'connect' })
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
