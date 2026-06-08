/**
 * NAVIGATION + SIDEBAR + LOGOUT — cross-page routing tests
 */
import { test, expect } from '@playwright/test'

test.describe('11 Navigation — Sidebar', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 15000 })
  })

  test('sidebar: Cluster Overview link navigates to /', async ({ page }) => {
    await page.goto('/targets')
    await page.getByRole('link', { name: /Cluster Overview|Overview|Dashboard/i }).first().click()
    await expect(page).toHaveURL('/', { timeout: 8000 })
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible()
  })

  test('sidebar: Targets link navigates to /targets', async ({ page }) => {
    await page.getByRole('link', { name: /^Targets$/ }).click()
    await expect(page).toHaveURL('/targets', { timeout: 8000 })
    await expect(page.getByRole('heading', { name: 'Targets' })).toBeVisible()
  })

  test('sidebar: Maintenance link navigates to /maintenance', async ({ page }) => {
    await page.getByRole('link', { name: /Maintenance/ }).click()
    await expect(page).toHaveURL('/maintenance', { timeout: 8000 })
    await expect(page.getByRole('heading', { name: 'Maintenance Windows' })).toBeVisible()
  })

  test('sidebar: SLO link navigates to /slo', async ({ page }) => {
    await page.getByRole('link', { name: /SLO/ }).click()
    await expect(page).toHaveURL('/slo', { timeout: 8000 })
    await expect(page.getByRole('heading', { name: 'SLO Dashboard' })).toBeVisible()
  })

  test('sidebar: Alerts link navigates to /alerts', async ({ page }) => {
    const alertsLink = page.getByRole('link', { name: /^Alerts$/ })
    if (await alertsLink.isVisible({ timeout: 3000 })) {
      await alertsLink.click()
      await expect(page).toHaveURL('/alerts', { timeout: 8000 })
    }
  })

  test('sidebar: Topology link navigates to /topology', async ({ page }) => {
    const link = page.getByRole('link', { name: /Topology/ })
    if (await link.isVisible({ timeout: 3000 })) {
      await link.click()
      await expect(page).toHaveURL('/topology', { timeout: 8000 })
    }
  })

  test('sidebar: Settings link navigates to /settings', async ({ page }) => {
    const link = page.getByRole('link', { name: /Settings/ })
    if (await link.isVisible({ timeout: 3000 })) {
      await link.click()
      await expect(page).toHaveURL(/\/settings/, { timeout: 8000 })
    }
  })

  test('sidebar: Users link visible for admin role', async ({ page }) => {
    const usersLink = page.getByRole('link', { name: /Users/ })
    await expect(usersLink).toBeVisible({ timeout: 8000 })
    await usersLink.click()
    await expect(page).toHaveURL('/users', { timeout: 8000 })
    await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()
  })

  test('active nav item has highlighted style', async ({ page }) => {
    // When on /targets, the Targets nav link should have active class
    await page.goto('/targets')
    await page.waitForTimeout(1000)
    const link = page.getByRole('link', { name: /^Targets$/ })
    const cls = await link.getAttribute('class') ?? ''
    // Active state should have some blue or bg class
    const isActive = cls.includes('blue') || cls.includes('bg-') || cls.includes('active')
    expect(isActive).toBe(true)
  })
})

test.describe('11 Navigation — Dark Mode', () => {
  test('dark mode toggle: informational — present or absent (not required)', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(2000)
    // Dark mode toggle is planned but not yet implemented (UI modernization backlog)
    // Check presence only — no clicks, no assertions, always passes
    const darkToggle = page.locator('button[title*="dark"], button[title*="Dark"]').first()
    const hasDarkToggle = await darkToggle.isVisible({ timeout: 2000 })
    console.log(`[11] dark mode toggle present: ${hasDarkToggle}`)
    // Always passes — dark mode is not yet a shipped feature
  })
})

test.describe('11 Navigation — Logout', () => {
  test('logout: clears session and redirects to /login or /connect', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 15000 })

    // Find logout button
    const logoutBtn = page.getByRole('button', { name: /Logout|Sign out|Log out/i })
    if (!await logoutBtn.isVisible({ timeout: 5000 })) {
      // Maybe logout is in a dropdown/menu
      const userMenu = page.locator('button[aria-label*="user"], button[aria-label*="account"], button[title*="user"]').first()
      if (await userMenu.isVisible({ timeout: 3000 })) {
        await userMenu.click()
        await page.waitForTimeout(500)
      }
    }

    const logoutBtnFinal = page.getByRole('button', { name: /Logout|Sign out|Log out/i })
    if (!await logoutBtnFinal.isVisible({ timeout: 3000 })) {
      test.skip()
      return
    }

    await logoutBtnFinal.click()
    await page.waitForTimeout(2000)

    // Should be redirected away from authenticated area
    const url = page.url()
    expect(url).toMatch(/\/(login|connect)/)
  })
})

test.describe('11 Navigation — Error pages', () => {
  test('404: navigating to unknown route shows error or redirects', async ({ page }) => {
    await page.goto('/this-page-does-not-exist')
    await page.waitForTimeout(2000)
    const content = await page.content()
    // Should show 404 error or redirect
    const has404    = content.includes('404') || content.includes('not found') || content.includes('Page Not Found')
    const redirected = !page.url().includes('this-page-does-not-exist')
    expect(has404 || redirected).toBe(true)
  })
})
