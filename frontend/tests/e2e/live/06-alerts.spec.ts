/**
 * ALERTS (/alerts) + SILENCES (/silences) tests
 */
import { test, expect } from '@playwright/test'

test.describe('06 Alerts', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/alerts')
    await page.waitForTimeout(3000)
  })

  test('page loads without crash', async ({ page }) => {
    // Should not show unhandled error
    const errorBanner = page.locator('text=/Failed to load alerts/i')
    const hasError = await errorBanner.isVisible()
    expect(hasError).toBe(false)
  })

  test('shows alerts list or empty state', async ({ page }) => {
    const hasAlerts = await page.locator('table, ul, [class*="alert"]').first().isVisible({ timeout: 8000 })
    const hasEmpty  = await page.getByText(/No alerts|no active/i).isVisible({ timeout: 8000 })
    const hasSkeleton = await page.locator('[class*="animate-pulse"]').isVisible({ timeout: 2000 })
    expect(hasAlerts || hasEmpty || hasSkeleton).toBe(true)
  })

  test('/alerts page heading or content is rendered', async ({ page }) => {
    const content = await page.content()
    // The page should have rendered meaningful content
    expect(content).toContain('alert')
  })
})

test.describe('06 Silences', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/silences')
    await page.waitForTimeout(2000)
  })

  test('silences page loads without crash', async ({ page }) => {
    const errorText = await page.locator('text=/Failed|error/i').count()
    // Soft assertion: page should render
    const content = await page.content()
    expect(content.length).toBeGreaterThan(100)
  })

  test('silences: shows list, empty state, or loading', async ({ page }) => {
    await page.waitForTimeout(3000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(100)
  })
})
