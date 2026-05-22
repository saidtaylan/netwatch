/**
 * useSLO tests — B17 breach detection logic.
 *
 * Polling itself is tested in usePolling.test.ts. Here we verify that
 * SLO breach transitions get pushed to the alerts store.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useNodesStore } from '~/stores/nodes'
import { useAlertsStore } from '~/stores/alerts'

function sloSnapshot(targets: Record<string, { slo_breached: boolean; actual_uptime: number; target_uptime: number; window: string }>) {
  return {
    computed_at: new Date().toISOString(),
    targets: Object.fromEntries(
      Object.entries(targets).map(([id, t]) => [
        id,
        {
          target_id:           id,
          target_uptime:       t.target_uptime,
          actual_uptime:       t.actual_uptime,
          window:              t.window,
          window_duration_sec: 86400,
          downtime_sec:        86400 * (1 - t.actual_uptime),
          downtime_minutes:    (86400 * (1 - t.actual_uptime)) / 60,
          incident_count:      t.slo_breached ? 1 : 0,
          slo_breached:        t.slo_breached,
          remaining_budget_sec: t.slo_breached ? -100 : 100,
        },
      ])
    ),
  }
}

describe('useSLO breach detection', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
    const nodes = useNodesStore()
    nodes.addNode('http://localhost:10240')
    nodes.setActive('http://localhost:10240')
    nodes.markHealthy('http://localhost:10240')
  })

  it('does not push alert on first observation', async () => {
    vi.mocked($fetch).mockResolvedValueOnce(
      sloSnapshot({ 'db-primary': { slo_breached: false, actual_uptime: 0.999, target_uptime: 0.995, window: '24h' } })
    )

    const { useSLO } = await import('~/composables/useSLO')
    const { slo } = useSLO()
    await slo.refresh()

    const alerts = useAlertsStore()
    expect(alerts.items).toHaveLength(0)
  })

  it('pushes BREACHED alert when slo_breached transitions false → true', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: false, actual_uptime: 0.999, target_uptime: 0.995, window: '24h' } }))
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: true,  actual_uptime: 0.900, target_uptime: 0.995, window: '24h' } }))

    const { useSLO } = await import('~/composables/useSLO')
    const { slo } = useSLO()
    await slo.refresh()
    await slo.refresh()

    const alerts = useAlertsStore()
    expect(alerts.items).toHaveLength(1)
    expect(alerts.items[0].id).toMatch(/^slo-/)
    expect(alerts.items[0].target_id).toBe('db-primary')
    expect(alerts.items[0].status).toBe('unreachable')   // semantic: breached
    expect(alerts.items[0].error_code).toContain('SLO breached')
  })

  it('pushes RECOVERED alert when slo_breached transitions true → false', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: true,  actual_uptime: 0.900, target_uptime: 0.995, window: '24h' } }))
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: false, actual_uptime: 0.999, target_uptime: 0.995, window: '24h' } }))

    const { useSLO } = await import('~/composables/useSLO')
    const { slo } = useSLO()
    await slo.refresh()
    await slo.refresh()

    const alerts = useAlertsStore()
    expect(alerts.items).toHaveLength(1)
    expect(alerts.items[0].status).toBe('reachable')
    expect(alerts.items[0].error_code).toContain('SLO recovered')
  })

  it('no alert when slo_breached stays the same across polls', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: false, actual_uptime: 0.999, target_uptime: 0.995, window: '24h' } }))
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: false, actual_uptime: 0.998, target_uptime: 0.995, window: '24h' } }))
      .mockResolvedValueOnce(sloSnapshot({ 'db-primary': { slo_breached: false, actual_uptime: 0.997, target_uptime: 0.995, window: '24h' } }))

    const { useSLO } = await import('~/composables/useSLO')
    const { slo } = useSLO()
    await slo.refresh()
    await slo.refresh()
    await slo.refresh()

    expect(useAlertsStore().items).toHaveLength(0)
  })

  it('handles multiple targets independently', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce(sloSnapshot({
        'db-primary':  { slo_breached: false, actual_uptime: 0.999, target_uptime: 0.995, window: '24h' },
        'api-gateway': { slo_breached: false, actual_uptime: 0.999, target_uptime: 0.995, window: '24h' },
      }))
      .mockResolvedValueOnce(sloSnapshot({
        'db-primary':  { slo_breached: true,  actual_uptime: 0.900, target_uptime: 0.995, window: '24h' },
        'api-gateway': { slo_breached: false, actual_uptime: 0.998, target_uptime: 0.995, window: '24h' },
      }))

    const { useSLO } = await import('~/composables/useSLO')
    const { slo } = useSLO()
    await slo.refresh()
    await slo.refresh()

    const alerts = useAlertsStore()
    expect(alerts.items).toHaveLength(1)
    expect(alerts.items[0].target_id).toBe('db-primary')
  })
})
