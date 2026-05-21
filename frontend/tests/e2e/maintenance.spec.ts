import { test, expect } from '@playwright/test'
import { mockAllRoutes, seedAuth } from './fixtures/api-mocks'

test.use({ storageState: { cookies: [], origins: [] } })

test.beforeEach(async ({ page }) => {
  await seedAuth(page)
  await mockAllRoutes(page)
})

test.describe('Maintenance Windows', () => {
  test('renders page heading', async ({ page }) => {
    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 10000 })
  })

  // SKIPPED: useMaintenance polling not firing in production build with route
  //          mocks — fleet polling works but maintenance polling doesn't return.
  test('shows empty state when no active windows ', async ({ page }) => {
    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('No maintenance windows')).toBeVisible({ timeout: 10000 })
  })

  test('New Window button opens create form', async ({ page }) => {
    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /New Window/ }).click()
    await expect(page.getByText('New Maintenance Window')).toBeVisible()
  })

  test('form accepts target and reason inputs', async ({ page }) => {
    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /New Window/ }).click()
    const input = page.locator('input[placeholder*="db-primary"]')
    await expect(input).toBeVisible()
    await input.fill('db-primary')
    await expect(input).toHaveValue('db-primary')
  })

  // SKIPPED: Toast appears after PUT response but page.route() PUT interception
  //          timing flaky in production build. Needs network condition wait.
  test('submitting form shows success toast ', async ({ page }) => {
    // Override maintenance PUT to return success
    await page.route('http://localhost:19240/cluster/maintenance', async r => {
      if (r.request().method() === 'PUT') {
        r.fulfill({
          json: {
            id: 'e2e-1', target_id: 'db-primary', reason: 'E2E test',
            started_at: new Date().toISOString(),
            ends_at: new Date(Date.now() + 3600000).toISOString(),
            created_by: 'ui',
          }
        })
      } else {
        r.fulfill({ json: [] })
      }
    })

    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /New Window/ }).click()

    await page.locator('input[placeholder*="db-primary"]').fill('db-primary')
    await page.locator('input[placeholder*="Scheduled"]').fill('E2E test maintenance')
    await page.getByRole('button', { name: 'Set Maintenance' }).click()

    await expect(page.getByText('Maintenance set for db-primary')).toBeVisible({ timeout: 10000 })
  })

  test('Cancel button in form hides it', async ({ page }) => {
    await page.goto('/maintenance')
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /New Window/ }).click()
    await expect(page.getByText('New Maintenance Window')).toBeVisible()
    // Click the Cancel button inside the form (not the New Window toggle)
    await page.getByRole('button', { name: 'Cancel' }).last().click()
    await expect(page.getByText('New Maintenance Window')).not.toBeVisible()
  })
})
