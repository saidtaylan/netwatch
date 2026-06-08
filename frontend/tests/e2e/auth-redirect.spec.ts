import { test, expect } from '@playwright/test'
import { mockAllRoutes } from './fixtures/api-mocks'

// Fresh context — no auth state
test.use({ storageState: { cookies: [], origins: [] } })

test.beforeEach(async ({ page }) => {
  await mockAllRoutes(page)
})

test.describe('Auth redirect (unauthenticated)', () => {
  test('/ redirects to /connect when no nodes configured', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/connect$/, { timeout: 10000 })
    await expect(page.getByRole('heading', { name: 'Connect to Netwatch' })).toBeVisible()
  })

  test('/targets redirects to /connect when not logged in', async ({ page }) => {
    await page.goto('/targets')
    await expect(page).toHaveURL(/\/(connect|login|setup)$/, { timeout: 10000 })
  })

  test('/connect shows backend URL input', async ({ page }) => {
    await page.goto('/connect')
    await expect(page.locator('input[type="url"]').first()).toBeVisible()
    await expect(page.getByRole('button', { name: /Connect/ })).toBeVisible()
  })

  test('/connect → /setup when backend not yet initialized', async ({ page }) => {
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill('http://localhost:19240')
    await page.getByRole('button', { name: /Connect/ }).click()
    // Mocked /auth/status starts with setup_completed:false → /setup
    await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })
    await expect(page.getByRole('heading', { name: /Initial Setup/ })).toBeVisible()
  })

  test('setup flow → creates admin → shows credentials → dashboard', async ({ page }) => {
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill('http://localhost:19240')
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })

    await page.locator('input[type="password"]').first().fill('setup-token-xyz')   // setup token
    await page.locator('input[type="text"]').first().fill('admin')                  // username
    await page.locator('input[type="password"]').nth(1).fill('strongpw1234')        // password
    await page.getByRole('button', { name: /Create Admin User/ }).click()

    await expect(page.getByText('Setup Complete!')).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /Go to Dashboard/ }).click()
    await expect(page).toHaveURL('/', { timeout: 10000 })
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })
  })
})
