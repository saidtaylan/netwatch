import { test, expect } from '@playwright/test'
import { mockAllRoutes } from './fixtures/api-mocks'

// Fresh context — no auth state
test.use({ storageState: { cookies: [], origins: [] } })

test.beforeEach(async ({ page }) => {
  await mockAllRoutes(page)
})

test.describe('Auth redirect (unauthenticated)', () => {
  test('/ redirects to /setup when not logged in', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL('/setup', { timeout: 10000 })
    await expect(page.getByText('Connect to Backend')).toBeVisible()
  })

  test('/targets redirects to /setup', async ({ page }) => {
    await page.goto('/targets')
    await expect(page).toHaveURL('/setup', { timeout: 10000 })
  })

  test('/setup shows backend URL and token inputs', async ({ page }) => {
    await page.goto('/setup')
    await expect(page.locator('input[type="url"]').first()).toBeVisible()
    await expect(page.locator('input[type="password"]')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Connect' })).toBeVisible()
  })

  test('successful login redirects to cluster overview', async ({ page }) => {
    await page.goto('/setup')
    await page.locator('input[type="url"]').first().fill('http://localhost:19240')
    await page.locator('input[type="password"]').fill('test-token')
    await page.locator('button[type="submit"]').click()
    await expect(page).toHaveURL('/', { timeout: 10000 })
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })
  })
})
