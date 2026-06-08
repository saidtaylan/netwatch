/**
 * zz-tour — captures full-page screenshots of the main pages for the README GIF.
 * Runs under playwright.live.config.ts (authenticated via live setup) against a
 * real cluster. Not a real assertion suite; it just drives the UI and snaps frames.
 *
 * Output: ../screenshots/frames/NN-name.png  (consumed by scripts/make-gif.py)
 */
import { test } from '@playwright/test'
import * as path from 'node:path'

const FRAMES = path.resolve(import.meta.dirname, '../../../../screenshots/frames')

const pages: Array<{ file: string; url: string }> = [
  { file: '01-overview', url: '/' },
  { file: '02-targets',  url: '/targets' },
  { file: '03-topology', url: '/topology' },
  { file: '04-slo',      url: '/slo' },
  { file: '05-geo',      url: '/geo' },
  { file: '06-config',   url: '/config' },
  { file: '07-users',    url: '/users' },
]

test('capture tour frames', async ({ page }) => {
  test.setTimeout(120_000)
  await page.setViewportSize({ width: 1440, height: 900 })

  for (const p of pages) {
    await page.goto(p.url)
    // Let data load + animations settle.
    await page.waitForLoadState('networkidle').catch(() => {})
    await page.waitForTimeout(1500)
    await page.screenshot({ path: path.join(FRAMES, `${p.file}.png`) })
  }
})
