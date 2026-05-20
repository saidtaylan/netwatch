import { describe, it, expect } from 'vitest'
import { stateStyle, isDown, STATE_STYLE, SCOPE_STYLE, CLASS_STYLE } from '~/utils/classifyState'
import type { TargetState } from '~/types/api'

const ALL_STATES: TargetState[] = ['up', 'soft_up', 'soft_down', 'hard_down', 'unknown']

describe('stateStyle', () => {
  it('returns a style object for every known state', () => {
    for (const s of ALL_STATES) {
      const style = stateStyle(s)
      expect(style).toBeDefined()
      expect(style.label).toBeTruthy()
      expect(style.color).toContain('text-')
      expect(style.bg).toContain('bg-')
      expect(style.icon).toBeTruthy()
    }
  })

  it('returns green color for up', () => {
    expect(stateStyle('up').color).toContain('green')
  })

  it('returns red color for hard_down', () => {
    expect(stateStyle('hard_down').color).toContain('red')
  })

  it('returns orange for soft_down', () => {
    expect(stateStyle('soft_down').color).toContain('orange')
  })

  it('falls back to unknown style for unrecognised state', () => {
    const style = stateStyle('nonexistent' as TargetState)
    expect(style).toEqual(STATE_STYLE.unknown)
  })
})

describe('isDown', () => {
  it('returns true for hard_down', () => {
    expect(isDown('hard_down')).toBe(true)
  })
  it('returns true for soft_down', () => {
    expect(isDown('soft_down')).toBe(true)
  })
  it('returns false for up', () => {
    expect(isDown('up')).toBe(false)
  })
  it('returns false for soft_up', () => {
    expect(isDown('soft_up')).toBe(false)
  })
  it('returns false for unknown', () => {
    expect(isDown('unknown')).toBe(false)
  })
})

describe('SCOPE_STYLE', () => {
  it('has entries for all scopes', () => {
    const scopes = ['GLOBAL', 'PARTIAL', 'NODE_LOCAL', 'STANDALONE'] as const
    for (const s of scopes) {
      expect(SCOPE_STYLE[s]).toBeDefined()
      expect(SCOPE_STYLE[s].label).toBeTruthy()
    }
  })
  it('GLOBAL is red', () => {
    expect(SCOPE_STYLE.GLOBAL.color).toContain('red')
  })
})

describe('CLASS_STYLE', () => {
  it('has entries for all classifications', () => {
    const classes = ['REAL_OUTAGE', 'NETWORK_PARTITION', 'LOCAL_FAILURE', 'AMBIGUOUS'] as const
    for (const c of classes) {
      expect(CLASS_STYLE[c]).toBeDefined()
      expect(CLASS_STYLE[c].label).toBeTruthy()
    }
  })
})
