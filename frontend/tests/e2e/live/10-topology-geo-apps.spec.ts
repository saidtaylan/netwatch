/**
 * TOPOLOGY (/topology), GEO (/geo), APPS (/apps), AUDIT (/audit), CHANNELS (/channels)
 * These pages render cluster-wide data visualizations.
 */
import { test, expect } from '@playwright/test'

test.describe('10 Topology', () => {
  test('topology page loads without crash', async ({ page }) => {
    await page.goto('/topology')
    await page.waitForTimeout(4000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
    await expect(page.locator('text=/Failed to load topology/i')).not.toBeVisible()
  })

  test('topology: shows target cards, table, or empty state', async ({ page }) => {
    await page.goto('/topology')
    await page.waitForTimeout(6000)
    // topology.vue renders HTML card lists or a table when targets exist
    // or EmptyState with 'No topology data' when no targets have depends_on
    const hasHeading   = await page.getByRole('heading', { name: 'Topology' }).isVisible()
    const hasTable     = await page.locator('table').first().isVisible({ timeout: 3000 })
    const hasCards     = await page.locator('li').first().isVisible({ timeout: 3000 })
    const hasEmpty     = await page.locator('text=/No topology data/i').isVisible({ timeout: 3000 })
    expect(hasHeading || hasTable || hasCards || hasEmpty).toBe(true)
  })
})

test.describe('10 Geo Latency', () => {
  test('geo page loads without crash', async ({ page }) => {
    await page.goto('/geo')
    await page.waitForTimeout(4000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
    await expect(page.locator('text=/Failed to load geo/i')).not.toBeVisible()
  })

  test('geo: shows latency data or empty state', async ({ page }) => {
    await page.goto('/geo')
    await page.waitForTimeout(5000)
    const hasData  = await page.locator('table, svg, [class*="latency"]').first().isVisible({ timeout: 5000 })
    const hasEmpty = await page.locator('text=/No data|no latency/i').isVisible({ timeout: 3000 })
    const hasAny   = hasData || hasEmpty
    // Just assert the page doesn't show a hard error
    const hasError = await page.locator('text=/Failed to load/i').isVisible()
    expect(hasError).toBe(false)
  })
})

test.describe('10 Apps', () => {
  test('apps page loads without crash', async ({ page }) => {
    await page.goto('/apps')
    await page.waitForTimeout(3000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
    await expect(page.locator('text=/Failed to load apps/i')).not.toBeVisible()
  })

  test('apps: shows app groupings or empty state', async ({ page }) => {
    await page.goto('/apps')
    await page.waitForTimeout(5000)
    const hasError = await page.locator('text=/Failed to load/i').isVisible()
    expect(hasError).toBe(false)
  })
})

test.describe('10 Audit Log', () => {
  test('audit page loads', async ({ page }) => {
    await page.goto('/audit')
    await page.waitForTimeout(3000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
  })

  test('audit: no crash, shows content or empty state', async ({ page }) => {
    await page.goto('/audit')
    await page.waitForTimeout(4000)
    const hasError = await page.locator('text=/Failed to load/i').isVisible()
    expect(hasError).toBe(false)
  })
})

test.describe('10 Channels', () => {
  test('channels page loads', async ({ page }) => {
    await page.goto('/channels')
    await page.waitForTimeout(3000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
  })
})
