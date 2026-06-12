import type { ClusterState, ConfigSyncSnapshot } from '~/types/api'

/** Polls cluster state (/cluster/state) and config-sync drift (/cluster/config),
 * returning both as reactive, auto-refreshing refs. */
export const useCluster = () => {
  const api = useApi()

  const clusterState = usePolling<ClusterState>(
    () => api.get<ClusterState>('/cluster/state')
  )

  const configSync = usePolling<ConfigSyncSnapshot>(
    () => api.get<ConfigSyncSnapshot>('/cluster/config'),
    { intervalMs: 15000 }
  )

  return { clusterState, configSync }
}
