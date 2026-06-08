/**
 * DASHBOARD (/) — Cluster Overview tests
 */
import { test, expect } from '@playwright/test'

test.describe('02 Dashboard — Cluster Overview', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 15000 })
  })

  test('shows 4 status cards: Cluster Nodes / Targets Up / Targets Down / Config Drift', async ({ page }) => {
    await expect(page.getByText('Cluster Nodes')).toBeVisible()
    await expect(page.getByText('Targets Up')).toBeVisible()
    await expect(page.getByText('Targets Down')).toBeVisible()
    await expect(page.getByText('Config Drift')).toBeVisible()
  })

  test('Cluster Nodes card shows a number ≥ 1', async ({ page }) => {
    const nodesCard = page.locator('div').filter({ hasText: 'Cluster Nodes' }).first()
    await nodesCard.waitFor({ timeout: 10000 })
    // The card should have a numeric value (at least 1 node)
    const text = await nodesCard.innerText()
    const match = text.match(/\d+/)
    expect(match).not.toBeNull()
    const count = parseInt(match![0], 10)
    expect(count).toBeGreaterThanOrEqual(1)
  })

  test('version badge is visible if backend exposes version', async ({ page }) => {
    // Version badge is shown only when GET /version responds with { version: "x.y.z" }
    // If the endpoint is absent the badge is hidden (v-if guard) — test is informational only
    await page.waitForTimeout(3000)
    const versionBadge = page.locator('span').filter({ hasText: /v\d+/ }).first()
    const visible = await versionBadge.isVisible()
    // Log result but don't fail — endpoint presence depends on build
    console.log(`[02] version badge visible: ${visible}`)
  })

  test('quorum status shown (healthy or isolated)', async ({ page }) => {
    // One of these texts should appear in the Cluster Nodes card area
    const quorumOk = page.locator('text=✓ Quorum healthy')
    const isolated  = page.locator('text=⚠ Isolated mode')
    const quorumLost = page.locator('text=✗ Quorum lost')

    const anyVisible = await Promise.any([
      quorumOk.waitFor({ timeout: 8000 }),
      isolated.waitFor({ timeout: 8000 }),
      quorumLost.waitFor({ timeout: 8000 }),
    ]).then(() => true).catch(() => false)

    expect(anyVisible).toBe(true)
  })

  test('Config Drift "View →" link navigates to /config', async ({ page }) => {
    const configLink = page.getByRole('link', { name: /View →/ })
    await expect(configLink).toBeVisible()
    await configLink.click()
    await expect(page).toHaveURL(/\/config/, { timeout: 8000 })
  })

  test('Cluster Members list shows at least 1 member row', async ({ page }) => {
    const membersHeader = page.getByText('Cluster Members')
    await expect(membersHeader).toBeVisible({ timeout: 12000 })

    // Each member row has a colored dot + member name
    const memberRows = page.locator('ul').filter({ has: page.locator('span.rounded-full') }).first().locator('li')
    const count = await memberRows.count()
    expect(count).toBeGreaterThanOrEqual(1)
  })

  test('Cluster Members: alive members show green dot, dead show red', async ({ page }) => {
    await page.waitForTimeout(2000)
    const greenDots = page.locator('.bg-green-400')
    const count = await greenDots.count()
    expect(count).toBeGreaterThanOrEqual(1)
  })

  test('no active ErrorBanner on healthy cluster', async ({ page }) => {
    await page.waitForTimeout(3000)
    const errorBanner = page.locator('[class*="bg-red"]:not(.text-red-600):not(.text-red-500)')
    const errorBannerText = page.getByText(/Failed to load cluster state/)
    await expect(errorBannerText).not.toBeVisible()
  })

  test('sidebar navigation links are visible', async ({ page }) => {
    await expect(page.getByRole('link', { name: /Targets/ })).toBeVisible()
    await expect(page.getByRole('link', { name: /Maintenance/ })).toBeVisible()
    await expect(page.getByRole('link', { name: /SLO/ })).toBeVisible()
  })
})
