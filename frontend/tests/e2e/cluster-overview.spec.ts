import { test, expect } from '@playwright/test'
import { mockAllRoutes, seedAuth } from './fixtures/api-mocks'

test.use({ storageState: { cookies: [], origins: [] } })

test.beforeEach(async ({ page }) => {
  await seedAuth(page)
  await mockAllRoutes(page)
})

test.describe('Cluster Overview', () => {
  test('renders heading', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 15000 })
  })

  test('renders stat cards', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByText('Targets Up')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('Targets Down')).toBeVisible()
    await expect(page.getByText('Config Drift')).toBeVisible()
  })

  test('shows down targets from fleet', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByText('🔴 Down Targets')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('db-primary')).toBeVisible()
  })

  // SKIPPED: Pinia hydration timing issue — `ui` store not yet hydrated when
  //          sidebar renders, so `sidebarCollapsed` defaults to whatever, making
  //          selectors flaky. Investigate when refactoring tests.
  test('sidebar nav is present ', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })
    // Sidebar renders navigation labels
    const sidebar = page.locator('aside')
    await expect(sidebar.getByText('Cluster Overview')).toBeVisible()
    await expect(sidebar.getByText('Targets')).toBeVisible()
    await expect(sidebar.getByText('Maintenance')).toBeVisible()
  })

  // SKIPPED: TopBar button selector flaky — multiple buttons in header,
  //          first() doesn't reliably target the color-mode toggle.
  test('dark mode toggle is clickable ', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })
    // TopBar has emoji button for dark mode (☀️/🌙)
    const header = page.locator('header')
    // Find any button in the header that's not the logout
    const btns = header.locator('button').filter({ hasNotText: 'Logout' })
    await expect(btns.first()).toBeVisible()
    await btns.first().click()
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible()
  })
})
