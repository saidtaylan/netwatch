/**
 * SLO DASHBOARD (/slo) — CRUD + budget display tests
 */
import { test, expect } from '@playwright/test'

const SLO_TARGET_ID = 'e2e-slo-target'

test.describe('05 SLO Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/slo')
    await expect(page.getByRole('heading', { name: 'SLO Dashboard' })).toBeVisible({ timeout: 15000 })
  })

  test('page loads: header visible', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'SLO Dashboard' })).toBeVisible()
  })

  test('+ New SLO Target button visible for authenticated user', async ({ page }) => {
    await expect(page.getByRole('button', { name: /New SLO Target/ })).toBeVisible()
  })

  test('Refresh button visible and clickable', async ({ page }) => {
    const refreshBtn = page.getByRole('button', { name: 'Refresh' })
    await expect(refreshBtn).toBeVisible()
    await refreshBtn.click()
    await page.waitForTimeout(1000)
    // Should not crash
    await expect(page.getByRole('heading', { name: 'SLO Dashboard' })).toBeVisible()
  })

  test('SLO page shows content: disabled banner, empty state, or target list', async ({ page }) => {
    await page.waitForTimeout(3000)
    // Any of these three states is valid
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    const emptyState     = page.getByText('No SLO targets yet')
    // If SLO has data, target cards are shown
    const hasData        = page.locator('[class*="rounded-xl"]').filter({ hasText: /Target Uptime/ })
    const hasContent = await disabledBanner.isVisible({ timeout: 5000 })
      || await emptyState.isVisible({ timeout: 5000 })
      || await hasData.first().isVisible({ timeout: 5000 })
    expect(hasContent).toBe(true)
  })

  test('create SLO target: opens JSON modal', async ({ page }) => {
    await page.getByRole('button', { name: /New SLO Target/ }).click()
    await expect(page.getByRole('dialog')).toBeVisible({ timeout: 5000 })
    // Use heading role to avoid strict mode clash with button label
    await expect(page.getByRole('dialog').getByRole('heading', { name: 'New SLO Target' })).toBeVisible()

    const textarea = page.locator('textarea')
    const content = await textarea.inputValue()
    expect(content).toContain('"id"')
    expect(content).toContain('"target_uptime"')
    expect(content).toContain('"window"')
  })

  test('create SLO target: Cancel closes modal', async ({ page }) => {
    await page.getByRole('button', { name: /New SLO Target/ }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('dialog')).not.toBeVisible()
  })

  test('create SLO target: invalid target_uptime → error toast', async ({ page }) => {
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    if (await disabledBanner.isVisible({ timeout: 3000 })) { test.skip(); return }

    await page.getByRole('button', { name: /New SLO Target/ }).click()
    const textarea = page.locator('textarea')
    await textarea.clear()
    await textarea.fill(JSON.stringify({ id: SLO_TARGET_ID, target_uptime: 1.5, window: '30d' }, null, 2))
    await page.getByRole('button', { name: /Create/ }).click()
    await page.waitForTimeout(2000)
    // Error appears in toast span (not "Error Budget" label)
    const errorToast = page.locator('[class*="toast"], [class*="alert"]').locator('text=/between 0 and 1/i')
    const errorSpan  = page.locator('span').filter({ hasText: /between 0 and 1/i }).first()
    const hasError   = await errorToast.isVisible({ timeout: 3000 }) || await errorSpan.isVisible({ timeout: 3000 })
    expect(hasError).toBe(true)
  })

  test('create SLO target: missing id → error', async ({ page }) => {
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    if (await disabledBanner.isVisible({ timeout: 3000 })) { test.skip(); return }

    await page.getByRole('button', { name: /New SLO Target/ }).click()
    const textarea = page.locator('textarea')
    await textarea.clear()
    await textarea.fill(JSON.stringify({ target_uptime: 0.99, window: '30d' }, null, 2))
    await page.getByRole('button', { name: /Create/ }).click()
    await page.waitForTimeout(2000)
    const errorSpan = page.locator('span').filter({ hasText: /needs an id/i }).first()
    const hasError  = await errorSpan.isVisible({ timeout: 3000 })
    expect(hasError).toBe(true)
  })

  test('create SLO target: valid target succeeds', async ({ page }) => {
    // Skip if SLO disabled
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    if (await disabledBanner.isVisible({ timeout: 3000 })) { test.skip(); return }

    // Delete existing SLO target first if it exists (leftover from prev run)
    const existingCard = page.locator('[class*="rounded-xl"]').filter({ hasText: SLO_TARGET_ID }).first()
    if (await existingCard.isVisible({ timeout: 2000 })) {
      page.on('dialog', d => d.accept())
      await existingCard.getByRole('button', { name: 'Delete' }).click()
      await page.waitForTimeout(2000)
    }

    await page.getByRole('button', { name: /New SLO Target/ }).click()
    const textarea = page.locator('textarea')
    await textarea.clear()
    await textarea.fill(JSON.stringify({
      id: SLO_TARGET_ID,
      target_uptime: 0.999,
      window: '30d',
    }, null, 2))
    await page.getByRole('button', { name: /Create/ }).click()
    await expect(page.locator('text=/created|success/i').first()).toBeVisible({ timeout: 8000 })
    await expect(page.locator('[class*="rounded-xl"]').filter({ hasText: SLO_TARGET_ID }).first()).toBeVisible({ timeout: 10000 })
  })

  test('SLO target card: shows target_uptime, actual_uptime, error budget, incidents', async ({ page }) => {
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    if (await disabledBanner.isVisible({ timeout: 3000 })) { test.skip(); return }

    const targetCard = page.locator('[class*="rounded-xl"]').filter({ hasText: SLO_TARGET_ID })
    if (!await targetCard.isVisible({ timeout: 5000 })) { test.skip(); return }

    await expect(targetCard.getByText('Target Uptime')).toBeVisible()
    await expect(targetCard.getByText('Actual Uptime')).toBeVisible()
    await expect(targetCard.getByText('Error Budget')).toBeVisible()
    await expect(targetCard.getByText('Incidents')).toBeVisible()
  })

  test('edit SLO target: opens modal with existing data', async ({ page }) => {
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    if (await disabledBanner.isVisible({ timeout: 3000 })) { test.skip(); return }

    const editBtn = page.locator('button', { hasText: 'Edit' }).first()
    if (!await editBtn.isVisible({ timeout: 5000 })) { test.skip(); return }

    await editBtn.click()
    await expect(page.getByRole('dialog')).toBeVisible()
    const textarea = page.locator('textarea')
    const content = await textarea.inputValue()
    expect(content).toContain('"target_uptime"')
  })

  test('delete SLO target: confirm dialog + removal', async ({ page }) => {
    const disabledBanner = page.getByText('SLO tracking is disabled.')
    if (await disabledBanner.isVisible({ timeout: 3000 })) { test.skip(); return }

    // Find the specific target card for our SLO test target
    const targetCard = page.locator('[class*="rounded-xl"]').filter({ hasText: SLO_TARGET_ID }).first()
    if (!await targetCard.isVisible({ timeout: 5000 })) { test.skip(); return }

    // Accept the browser confirm dialog
    page.on('dialog', dialog => dialog.accept())
    await targetCard.getByRole('button', { name: 'Delete' }).click()

    await expect(page.locator('text=/deleted|success/i').first()).toBeVisible({ timeout: 8000 })
    await page.waitForTimeout(2000)
    // The card for our specific target should be gone
    await expect(page.locator('[class*="rounded-xl"]').filter({ hasText: SLO_TARGET_ID })).not.toBeVisible()
  })
})
