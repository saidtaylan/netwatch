import { test, expect } from '@playwright/test'
import { mockAllRoutes, seedAuth } from './fixtures/api-mocks'

test.use({ storageState: { cookies: [], origins: [] } })

test.beforeEach(async ({ page }) => {
  await seedAuth(page)
  await mockAllRoutes(page)
})

test.describe('Error page', () => {
  test('unknown route shows 404 error page', async ({ page }) => {
    await page.goto('/this-route-does-not-exist')
    await expect(page.getByText('404')).toBeVisible({ timeout: 10000 })
    // Scoped to the heading — getByText('Page not found') also matches the
    // error-detail <pre> block ("Error: Page not found: /this-route-...."),
    // which is a Playwright strict-mode violation (2 matches).
    await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible()
  })

  test('error page has "Go to Cluster Overview" button when authenticated', async ({ page }) => {
    await page.goto('/non-existent')
    await expect(page.getByRole('button', { name: /Cluster Overview/ })).toBeVisible({ timeout: 10000 })
  })

  test('clicking go home returns to cluster overview', async ({ page }) => {
    await page.goto('/missing')
    await expect(page.getByText('404')).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /Cluster Overview/ }).click()
    await expect(page).toHaveURL('/', { timeout: 10000 })
  })
})
