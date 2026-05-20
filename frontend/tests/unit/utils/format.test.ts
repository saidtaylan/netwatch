import { describe, it, expect } from 'vitest'
import { fmtDurationSec, fmtPercent, fmtLatency, capitalize } from '~/utils/format'

describe('fmtDurationSec', () => {
  it('formats zero as empty string → 0s', () => {
    expect(fmtDurationSec(0)).toBe('0s')
  })
  it('formats 90 seconds as "1 minute 30 seconds"', () => {
    expect(fmtDurationSec(90)).toContain('minute')
  })
  it('formats 3661 seconds as hours + minutes + seconds', () => {
    const out = fmtDurationSec(3661)
    expect(out).toContain('hour')
    expect(out).toContain('minute')
  })
  it('handles negative → prepends −', () => {
    expect(fmtDurationSec(-60)).toMatch(/^−/)
  })
})

describe('fmtPercent', () => {
  it('formats 0.999 as "99.90%"', () => {
    expect(fmtPercent(0.999)).toBe('99.90%')
  })
  it('formats 1.0 as "100.00%"', () => {
    expect(fmtPercent(1.0)).toBe('100.00%')
  })
  it('respects decimal places', () => {
    expect(fmtPercent(0.9991, 3)).toBe('99.910%')
  })
  it('formats 0 as "0.00%"', () => {
    expect(fmtPercent(0)).toBe('0.00%')
  })
})

describe('fmtLatency', () => {
  it('formats 0.050 → "50ms"', () => {
    expect(fmtLatency(0.05)).toBe('50ms')
  })
  it('formats 0.001 → "1ms"', () => {
    expect(fmtLatency(0.001)).toBe('1ms')
  })
  it('formats 1.5 → "1.50s"', () => {
    expect(fmtLatency(1.5)).toBe('1.50s')
  })
  it('formats 0 → "0ms"', () => {
    expect(fmtLatency(0)).toBe('0ms')
  })
})

describe('capitalize', () => {
  it('capitalizes first letter', () => {
    expect(capitalize('hello')).toBe('Hello')
  })
  it('handles empty string', () => {
    expect(capitalize('')).toBe('')
  })
  it('leaves already-capitalized string unchanged', () => {
    expect(capitalize('Hello')).toBe('Hello')
  })
})
