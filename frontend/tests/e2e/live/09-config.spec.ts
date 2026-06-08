/**
 * CONFIG — /config (sync), /config/push, /config/keyring
 */
import { test, expect } from '@playwright/test'

test.describe('09 Config Sync', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/config')
    await page.waitForTimeout(3000)
  })

  test('config page loads without crash', async ({ page }) => {
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
    await expect(page.locator('text=/Failed to load config/i')).not.toBeVisible()
  })

  test('shows config sync state: All same, differ, unreachable, or loading', async ({ page }) => {
    await page.waitForTimeout(5000)
    // config/index.vue renders: '✓ All same' when no drift, '⚠ N differ' when drift, 'unreachable', or loading
    const allSame     = page.locator('text=/All same/i')
    const hasDiff     = page.locator('text=/\\d+ differ/i')
    const unreachable = page.locator('text=/unreachable/i')
    const loading     = page.locator('text=/Loading cluster/i')
    const hasAny = await allSame.isVisible({ timeout: 5000 })
      || await hasDiff.isVisible({ timeout: 2000 })
      || await unreachable.isVisible({ timeout: 2000 })
      || await loading.isVisible({ timeout: 2000 })
    expect(hasAny).toBe(true)
  })
})

test.describe('09 Config Push', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/config/push')
    await page.waitForTimeout(2000)
  })

  test('config push page loads', async ({ page }) => {
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
  })

  test('push page shows form or node selector', async ({ page }) => {
    const hasForm = await page.locator('form, textarea, select, input').first().isVisible({ timeout: 5000 })
    const hasContent = await page.locator('h1, h2, h3').first().isVisible({ timeout: 5000 })
    expect(hasForm || hasContent).toBe(true)
  })
})

test.describe('09 Config Keyring', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/config/keyring')
    await page.waitForTimeout(2000)
  })

  test('keyring page loads', async ({ page }) => {
    const content = await page.content()
    expect(content.length).toBeGreaterThan(200)
  })
})
