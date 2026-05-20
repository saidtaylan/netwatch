import { formatDuration, intervalToDuration, formatDistanceToNow, parseISO } from 'date-fns'

/** Format seconds → "2h 15m 30s" */
export function fmtDurationSec(seconds: number): string {
  if (seconds < 0) return `−${fmtDurationSec(-seconds)}`
  const dur = intervalToDuration({ start: 0, end: seconds * 1000 })
  return formatDuration(dur, { delimiter: ' ' }) || '0s'
}

/** Format ratio 0-1 → "99.90%" */
export function fmtPercent(ratio: number, decimals = 2): string {
  return `${(ratio * 100).toFixed(decimals)}%`
}

/** Format ISO string → relative time "3 minutes ago" */
export function fmtRelative(iso: string): string {
  try { return formatDistanceToNow(parseISO(iso), { addSuffix: true }) }
  catch { return iso }
}

/** Format latency in seconds → "123ms" or "1.23s" */
export function fmtLatency(seconds: number): string {
  const ms = seconds * 1000
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${seconds.toFixed(2)}s`
}

/** Capitalize first letter */
export function capitalize(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : ''
}
