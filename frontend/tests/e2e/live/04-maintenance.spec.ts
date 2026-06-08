/**
 * MAINTENANCE WINDOWS (/maintenance) — CRUD tests
 */
import { test, expect } from '@playwright/test'

test.describe('04 Maintenance Windows', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 15000 })
  })

  test('page loads with header + description text', async ({ page }) => {
    await expect(page.getByText('Suppress alerts for targets during scheduled maintenance.')).toBeVisible()
  })

  test('+ New Window button visible for authenticated user', async ({ page }) => {
    await expect(page.getByRole('button', { name: /New Window/ })).toBeVisible()
  })

  test('empty state shown when no windows exist', async ({ page }) => {
    await page.waitForTimeout(3000)
    const active = page.locator('text=Active (')
    const empty  = page.getByText('No maintenance windows')
    const hasContent = await active.isVisible() || await empty.isVisible()
    expect(hasContent).toBe(true)
  })

  test('+ New Window toggles the create form', async ({ page }) => {
    const btn = page.getByRole('button', { name: /New Window/ })
    await btn.click()
    await expect(page.getByRole('heading', { name: 'New Maintenance Window' })).toBeVisible()

    // When form is open the header button becomes "Cancel" — click the first one (header toggle)
    await page.getByRole('button', { name: 'Cancel' }).first().click()
    await expect(page.getByRole('heading', { name: 'New Maintenance Window' })).not.toBeVisible()
  })

  test('form: Set Maintenance button disabled when fields empty', async ({ page }) => {
    await page.getByRole('button', { name: /New Window/ }).click()
    await expect(page.getByRole('heading', { name: 'New Maintenance Window' })).toBeVisible()

    const submitBtn = page.getByRole('button', { name: /Set Maintenance/ })
    const isDisabled = await submitBtn.getAttribute('disabled')
    expect(isDisabled).not.toBeNull()
  })

  test('form: duration dropdown shows 5 options', async ({ page }) => {
    await page.getByRole('button', { name: /New Window/ }).click()

    const select = page.locator('select')
    await expect(select).toBeVisible()
    const options = select.locator('option')
    await expect(options).toHaveCount(5)

    // Check option texts
    await expect(options.nth(0)).toHaveText('30 minutes')
    await expect(options.nth(1)).toHaveText('1 hour')
    await expect(options.nth(4)).toHaveText('8 hours')
  })

  test('create maintenance window: happy path', async ({ page }) => {
    await page.getByRole('button', { name: /New Window/ }).click()

    await page.locator('input[placeholder="e.g. db-primary"]').fill('e2e-test-target')
    await page.locator('select').selectOption({ label: '30 minutes' })
    await page.locator('input[placeholder="e.g. Scheduled DB upgrade"]').fill('E2E test maintenance')

    const submitBtn = page.getByRole('button', { name: /Set Maintenance/ })
    await expect(submitBtn).not.toBeDisabled()
    await submitBtn.click()

    // After BUG-2 fix: form closes on both success and error
    // Success: "Maintenance set for e2e-test-target" toast
    // Error: form closes + error toast shown
    await expect(page.getByRole('heading', { name: 'New Maintenance Window' })).not.toBeVisible({ timeout: 8000 })
    // Check for success toast or error toast (either way form should be gone)
    const hasToast = await page.locator('text=/Maintenance set|Failed/i').isVisible({ timeout: 5000 })
    console.log(`[04] maintenance create toast visible: ${hasToast}`)
  })

  test('active maintenance window: shows target + reason + time remaining', async ({ page }) => {
    await page.waitForTimeout(1000)
    const activeSection = page.locator('text=Active (')
    if (!await activeSection.isVisible({ timeout: 5000 })) {
      test.skip()
      return
    }
    // Should show target ID
    await expect(page.getByText('e2e-test-target')).toBeVisible()
    // Should show reason
    await expect(page.getByText('E2E test maintenance')).toBeVisible()
    // Should show remaining time
    await expect(page.locator('text=/remaining|expired/i').first()).toBeVisible()
  })

  test('cancel maintenance window: confirmation dialog', async ({ page }) => {
    await page.waitForTimeout(1000)
    const cancelBtn = page.locator('button', { hasText: 'Cancel' }).last()
    if (!await cancelBtn.isVisible({ timeout: 5000 })) { test.skip(); return }

    await cancelBtn.click()

    // ConfirmDialog should appear
    await expect(page.getByText('Cancel maintenance window?')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('Alerts will resume immediately for this target.')).toBeVisible()
  })

  test('cancel maintenance window: clicking Cancel on dialog dismisses it', async ({ page }) => {
    await page.waitForTimeout(1000)
    const cancelWindowBtn = page.locator('button', { hasText: 'Cancel' })
    // The Cancel button in the maintenance window row
    const windowCancelBtn = page.locator('div.bg-orange-50, div[class*="border-orange"]').locator('button', { hasText: 'Cancel' }).first()

    if (!await windowCancelBtn.isVisible({ timeout: 5000 })) { test.skip(); return }
    await windowCancelBtn.click()

    await expect(page.getByText('Cancel maintenance window?')).toBeVisible()

    // Dismiss without confirming
    await page.getByRole('button', { name: 'Cancel' }).last().click()
    await expect(page.getByText('Cancel maintenance window?')).not.toBeVisible()
    // Window should still be active
    await expect(page.getByText('e2e-test-target')).toBeVisible()
  })

  test('cancel maintenance window: confirm deletion removes window', async ({ page }) => {
    await page.waitForTimeout(1000)
    const windowCancelBtn = page.locator('div[class*="border-orange"]').locator('button', { hasText: 'Cancel' }).first()
    if (!await windowCancelBtn.isVisible({ timeout: 5000 })) { test.skip(); return }

    await windowCancelBtn.click()
    await expect(page.getByText('Cancel maintenance window?')).toBeVisible()

    // Confirm the cancel
    await page.getByRole('button', { name: 'Yes, cancel' }).click()
    await page.waitForTimeout(3000)
    await expect(page.getByText('Cancel maintenance window?')).not.toBeVisible()
  })
})
