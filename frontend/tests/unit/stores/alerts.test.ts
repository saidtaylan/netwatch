import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAlertsStore } from '~/stores/alerts'
import type { AlertEntry } from '~/types/api'

function makeAlert(overrides: Partial<AlertEntry> = {}): AlertEntry {
  return {
    id:             '1',
    target_id:      'db-primary',
    target_name:    'DB Primary',
    target_type:    'tcp',
    status:         'unreachable',
    scope:          'GLOBAL',
    classification: 'REAL_OUTAGE',
    confidence:     0.95,
    seq:            1,
    timestamp:      '2026-05-20T10:00:00Z',
    ...overrides,
  }
}

describe('useAlertsStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('starts empty', () => {
    const store = useAlertsStore()
    expect(store.items).toHaveLength(0)
    expect(store.unresolvedCount).toBe(0)
  })

  it('push adds alert', () => {
    const store = useAlertsStore()
    store.push(makeAlert())
    expect(store.items).toHaveLength(1)
  })

  it('push deduplicates by target_id + seq', () => {
    const store = useAlertsStore()
    store.push(makeAlert({ id: '1', target_id: 'db', seq: 5 }))
    store.push(makeAlert({ id: '2', target_id: 'db', seq: 5 }))
    expect(store.items).toHaveLength(1)
  })

  it('push with different seq adds new entry', () => {
    const store = useAlertsStore()
    store.push(makeAlert({ id: '1', seq: 5 }))
    store.push(makeAlert({ id: '2', seq: 6 }))
    expect(store.items).toHaveLength(2)
  })

  it('push newest first (unshift)', () => {
    const store = useAlertsStore()
    store.push(makeAlert({ id: '1', seq: 1 }))
    store.push(makeAlert({ id: '2', seq: 2 }))
    expect(store.items[0].seq).toBe(2)
  })

  it('caps at MAX_ALERTS (100)', () => {
    const store = useAlertsStore()
    for (let i = 0; i < 110; i++) {
      store.push(makeAlert({ id: String(i), seq: i }))
    }
    expect(store.items).toHaveLength(100)
  })

  it('ack sets acked flag', () => {
    const store = useAlertsStore()
    store.push(makeAlert({ id: 'x' }))
    store.ack('x')
    expect(store.items[0].acked).toBe(true)
  })

  it('mute sets muted flag', () => {
    const store = useAlertsStore()
    store.push(makeAlert({ id: 'x' }))
    store.mute('x')
    expect(store.items[0].muted).toBe(true)
  })

  it('unresolvedCount counts non-acked unreachable alerts', () => {
    const store = useAlertsStore()
    store.push(makeAlert({ id: '1', seq: 1, status: 'unreachable' }))
    store.push(makeAlert({ id: '2', seq: 2, status: 'unreachable' }))
    store.push(makeAlert({ id: '3', seq: 3, status: 'reachable' }))
    expect(store.unresolvedCount).toBe(2)
    store.ack('1')
    expect(store.unresolvedCount).toBe(1)
  })

  it('clear empties items', () => {
    const store = useAlertsStore()
    store.push(makeAlert())
    store.clear()
    expect(store.items).toHaveLength(0)
  })

  it('recent returns up to 20 items', () => {
    const store = useAlertsStore()
    for (let i = 0; i < 30; i++) {
      store.push(makeAlert({ id: String(i), seq: i }))
    }
    expect(store.recent).toHaveLength(20)
  })
})
