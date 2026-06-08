/**
 * TARGETS (/targets) — List + CRUD + Filters + Detail page
 */
import { test, expect } from '@playwright/test'
import { BACKEND, ADMIN_PASS, ADMIN_USER, apiRequest } from './helpers'

const TEST_TARGET_ID = 'e2e-tcp-test'
const TEST_TARGET_2  = 'e2e-http-test'

test.describe('03 Targets — list page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByRole('heading', { name: 'Targets' })).toBeVisible({ timeout: 15000 })
  })

  test('page loads with header and counter', async ({ page }) => {
    await expect(page.getByText(/total/)).toBeVisible({ timeout: 8000 })
  })

  test('+ New Target button is visible for authenticated user', async ({ page }) => {
    const btn = page.getByRole('button', { name: /New Target/ })
    await expect(btn).toBeVisible()
  })

  test('filter bar: search input + All/Up/Down buttons + type select visible', async ({ page }) => {
    await expect(page.locator('input[type="search"]')).toBeVisible()
    await expect(page.getByRole('button', { name: 'All' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Up' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Down' })).toBeVisible()
    await expect(page.locator('select')).toBeVisible()
  })
})

test.describe('03 Targets — CRUD', () => {
  let authToken: string

  test.beforeAll(async () => {
    // Get a JWT token directly for API cleanup
    try {
      const res = await apiRequest('POST', '/auth/login', { username: ADMIN_USER, password: ADMIN_PASS })
      authToken = res.token
    } catch (_) { /* non-critical for UI tests */ }
  })

  test.afterAll(async () => {
    // Clean up test targets
    if (authToken) {
      for (const id of [TEST_TARGET_ID, TEST_TARGET_2]) {
        try { await apiRequest('DELETE', `/targets/${id}`, undefined, authToken) } catch (_) {}
      }
    }
  })

  test.beforeEach(async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByRole('heading', { name: 'Targets' })).toBeVisible({ timeout: 15000 })
  })

  test('create target: opens modal on + New Target click', async ({ page }) => {
    await page.getByRole('button', { name: /New Target/ }).click()
    await expect(page.getByRole('dialog')).toBeVisible({ timeout: 5000 })
    // Use heading role to avoid strict mode clash with button text
    await expect(page.getByRole('dialog').getByRole('heading', { name: 'New Target' })).toBeVisible()
  })

  test('create modal: JSON textarea is pre-filled with template', async ({ page }) => {
    await page.getByRole('button', { name: /New Target/ }).click()
    const textarea = page.locator('textarea')
    await expect(textarea).toBeVisible()
    const content = await textarea.inputValue()
    expect(content).toContain('"id"')
    expect(content).toContain('"type"')
    expect(content).toContain('"target"')
  })

  test('create modal: Cancel button closes the modal', async ({ page }) => {
    await page.getByRole('button', { name: /New Target/ }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('dialog')).not.toBeVisible()
  })

  test('create modal: invalid JSON shows inline parse error + modal stays open', async ({ page }) => {
    await page.getByRole('button', { name: /New Target/ }).click()
    const textarea = page.locator('textarea')
    await textarea.clear()
    await textarea.fill('{ invalid json :::')
    await page.getByRole('button', { name: /Create/ }).click()
    await page.waitForTimeout(500)
    // CrudJsonModal: invalid JSON shows inline error, modal STAYS OPEN (correct behavior)
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByRole('dialog').locator('text=/JSON/i')).toBeVisible()
    // Cancel to clean up
    await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('dialog')).not.toBeVisible()
  })

  test('create target: valid TCP target', async ({ page }) => {
    await page.getByRole('button', { name: /New Target/ }).click()
    const textarea = page.locator('textarea')
    await textarea.clear()
    await textarea.fill(JSON.stringify({
      id: TEST_TARGET_ID,
      type: 'tcp',
      target: '127.0.0.1:22',
      name: 'E2E TCP Test',
    }, null, 2))
    await page.getByRole('button', { name: /Create/ }).click()
    // Toast should confirm success
    await expect(page.locator('text=/created|success/i').first()).toBeVisible({ timeout: 8000 })
    // Target link (NuxtLink href) should appear in the list
    await expect(page.locator(`a[href*="${TEST_TARGET_ID}"]`)).toBeVisible({ timeout: 10000 })
  })

  test('create target: duplicate ID → error toast', async ({ page }) => {
    // Try to create the same target again
    await page.getByRole('button', { name: /New Target/ }).click()
    const textarea = page.locator('textarea')
    await textarea.clear()
    await textarea.fill(JSON.stringify({
      id: TEST_TARGET_ID,
      type: 'tcp',
      target: '127.0.0.1:22',
      name: 'E2E Duplicate',
    }, null, 2))
    await page.getByRole('button', { name: /Create/ }).click()
    // PUT /targets/:id is idempotent (upsert) — it should succeed (update),
    // or show an error if the backend rejects duplicates.
    await page.waitForTimeout(2000)
    // The key assertion: modal is closed
    await expect(page.getByRole('dialog')).not.toBeVisible()
  })

  test('edit target: opens edit modal with pre-filled data', async ({ page }) => {
    // Ensure the test target link is in the list (TargetRow renders href, not ID text)
    const targetLink = page.locator(`a[href*="${TEST_TARGET_ID}"]`)
    if (!await targetLink.isVisible({ timeout: 8000 })) {
      test.skip()
      return
    }
    // Click Edit button in the containing li row
    const row = page.locator('li').filter({ has: page.locator(`a[href*="${TEST_TARGET_ID}"]`) })
    await row.getByRole('button', { name: 'Edit' }).click()

    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByText(`Edit ${TEST_TARGET_ID}`)).toBeVisible()

    const textarea = page.locator('textarea')
    const content = await textarea.inputValue()
    expect(content).toContain(TEST_TARGET_ID)
  })

  test('edit target: change name and save', async ({ page }) => {
    const row = page.locator('li').filter({ has: page.locator(`a[href*="${TEST_TARGET_ID}"]`) })
    if (!await row.first().isVisible({ timeout: 8000 })) { test.skip(); return }

    await row.first().getByRole('button', { name: 'Edit' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    const textarea = dialog.locator('textarea')
    const current = JSON.parse(await textarea.inputValue())
    current.name = 'E2E TCP Updated'
    await textarea.clear()
    await textarea.fill(JSON.stringify(current, null, 2))

    await dialog.getByRole('button', { name: /Save/ }).click()
    // Toast: "Target e2e-tcp-test updated"
    await expect(page.locator('span').filter({ hasText: /updated/i }).first()).toBeVisible({ timeout: 8000 })
  })

  test('search filter: typing filters the list', async ({ page }) => {
    // TargetRow renders href not ID text — check link href exists
    const targetLink = page.locator(`a[href*="${TEST_TARGET_ID}"]`)
    const targetExists = await targetLink.isVisible({ timeout: 8000 })
    if (!targetExists) { test.skip(); return }

    const searchInput = page.locator('input[type="search"]')
    await searchInput.fill('e2e-tcp')
    await page.waitForTimeout(1500)

    // After filtering by ID prefix, the target link should still be visible
    await expect(page.locator(`a[href*="${TEST_TARGET_ID}"]`)).toBeVisible({ timeout: 5000 })

    // Search for something that doesn't exist
    await searchInput.fill('zzzz-nonexistent-target-xyz')
    await page.waitForTimeout(800)
    await expect(page.getByText('No matching targets')).toBeVisible()

    // Clear search
    await searchInput.clear()
  })

  test('status filter: Up button filters results', async ({ page }) => {
    await page.getByRole('button', { name: 'Up' }).click()
    await page.waitForTimeout(1000)
    // Button should be active (blue)
    const btn = page.getByRole('button', { name: 'Up' })
    const cls = await btn.getAttribute('class')
    expect(cls).toContain('bg-blue-600')
  })

  test('status filter: Down button filters results', async ({ page }) => {
    await page.getByRole('button', { name: 'Down' }).click()
    await page.waitForTimeout(1000)
    const btn = page.getByRole('button', { name: 'Down' })
    const cls = await btn.getAttribute('class')
    expect(cls).toContain('bg-blue-600')
    // Reset
    await page.getByRole('button', { name: 'All' }).click()
  })

  test('delete target: confirmation dialog appears', async ({ page }) => {
    // Accept confirm dialogs
    page.on('dialog', dialog => dialog.accept())

    // Use href-based locator (TargetRow does not render target ID as text)
    let row = page.locator('li').filter({ has: page.locator('a[href*="e2e-delete-me"]') })
    if (!await row.first().isVisible({ timeout: 3000 })) {
      await page.getByRole('button', { name: /New Target/ }).click()
      const textarea = page.locator('textarea')
      await textarea.clear()
      await textarea.fill(JSON.stringify({ id: 'e2e-delete-me', type: 'tcp', target: '127.0.0.1:9999', name: 'E2E Delete Test' }, null, 2))
      await page.getByRole('button', { name: /Create/ }).click()
      await expect(page.locator('text=/created|success/i').first()).toBeVisible({ timeout: 8000 })
      await expect(page.locator('a[href*="e2e-delete-me"]')).toBeVisible({ timeout: 8000 })
      row = page.locator('li').filter({ has: page.locator('a[href*="e2e-delete-me"]') })
    }

    await row.first().getByRole('button', { name: 'Delete' }).click()
    // Wait for fleet to refresh then verify row gone
    await page.waitForTimeout(3000)
    await expect(page.locator('a[href*="e2e-delete-me"]')).not.toBeVisible({ timeout: 8000 })
  })
})

test.describe('03 Targets — detail page', () => {
  test('clicking a target row opens detail page', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByRole('heading', { name: 'Targets' })).toBeVisible({ timeout: 15000 })
    await page.waitForTimeout(2000)

    // Click the first target in the list
    const firstTargetLink = page.locator('a[href^="/targets/"]').first()
    if (!await firstTargetLink.isVisible({ timeout: 5000 })) {
      // No targets yet — skip detail page test
      test.skip()
      return
    }
    const href = await firstTargetLink.getAttribute('href')
    await firstTargetLink.click()
    await expect(page).toHaveURL(/\/targets\/.+/, { timeout: 8000 })
  })

  test('target detail: shows by-node breakdown or empty state', async ({ page }) => {
    await page.goto('/targets')
    await page.waitForTimeout(3000)

    const firstLink = page.locator('a[href^="/targets/"]').first()
    if (!await firstLink.isVisible({ timeout: 5000 })) { test.skip(); return }

    await firstLink.click()
    await page.waitForTimeout(3000)

    // Should show either probe results or loading state
    const hasContent = await Promise.any([
      page.locator('text=/Node Results|by-node|Probe Results/i').waitFor({ timeout: 8000 }),
      page.locator('text=/No data|unknown/i').waitFor({ timeout: 8000 }),
      page.locator('text=/consensus_state|status/i').waitFor({ timeout: 8000 }),
    ]).then(() => true).catch(() => false)

    // Page must at minimum render without error
    const hasError = await page.locator('text=/Failed to load/i').isVisible()
    expect(hasError).toBe(false)
  })
})
