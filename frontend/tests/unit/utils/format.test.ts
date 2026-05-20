import { describe, it, expect } from 'vitest'
import { fmtDurationSec, fmtPercent, fmtLatency, capitalize } from '~/utils/format'

describe('fmtDurationSec', () => {
  it('formats zero → "0s"', () => {
    expect(fmtDurationSec(0)).toBe('0s')
  })
  it('formats 90 seconds', () => {
    expect(fmtDurationSec(90)).toContain('minute')
  })
  it('formats 3661 → contains hour + minute', () => {
    const out = fmtDurationSec(3661)
    expect(out).toContain('hour')
    expect(out).toContain('minute')
  })
  it('negative → prepends −', () => {
    expect(fmtDurationSec(-60)).toMatch(/^−/)
  })
  it('negative value mirrors positive', () => {
    expect(fmtDurationSec(-90)).toBe('−' + fmtDurationSec(90))
  })
})

describe('fmtPercent', () => {
  it('formats 0.999 → "99.90%"', () => {
    expect(fmtPercent(0.999)).toBe('99.90%')
  })
  it('formats 1.0 → "100.00%"', () => {
    expect(fmtPercent(1.0)).toBe('100.00%')
  })
  it('respects custom decimal places', () => {
    expect(fmtPercent(0.9991, 3)).toBe('99.910%')
  })
  it('formats 0 → "0.00%"', () => {
    expect(fmtPercent(0)).toBe('0.00%')
  })
})

describe('fmtLatency', () => {
  it('< 1s → ms', () => {
    expect(fmtLatency(0.05)).toBe('50ms')
    expect(fmtLatency(0.001)).toBe('1ms')
  })
  it('>= 1s → seconds', () => {
    expect(fmtLatency(1.5)).toBe('1.50s')
  })
  it('exactly 0 → "0ms"', () => {
    expect(fmtLatency(0)).toBe('0ms')
  })
  it('exactly 1s → "1.00s"', () => {
    expect(fmtLatency(1)).toBe('1.00s')
  })
})

describe('capitalize', () => {
  it('capitalizes first letter', () => {
    expect(capitalize('hello')).toBe('Hello')
  })
  it('handles empty string', () => {
    expect(capitalize('')).toBe('')
  })
  it('leaves already-capitalized unchanged', () => {
    expect(capitalize('Hello')).toBe('Hello')
  })
  it('handles single char', () => {
    expect(capitalize('a')).toBe('A')
  })
})
