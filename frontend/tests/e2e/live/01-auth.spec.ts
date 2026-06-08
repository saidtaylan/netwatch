/**
 * AUTH FLOW TESTS — connect / setup / login / logout
 * These run in a FRESH context (no storageState) to test the auth wizard.
 */
import { test, expect } from '@playwright/test'
import { BACKEND, SETUP_TOKEN, ADMIN_USER, ADMIN_PASS } from './helpers'

// Override: these tests must NOT use the pre-authenticated state
test.use({ storageState: { cookies: [], origins: [] } })

test.describe('01 Auth — redirect rules', () => {
  test('unauthenticated / → redirects to /connect', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/(connect|login)$/, { timeout: 10000 })
  })

  test('unauthenticated /targets → redirects to /connect or /login', async ({ page }) => {
    await page.goto('/targets')
    await expect(page).toHaveURL(/\/(connect|login)$/, { timeout: 10000 })
  })

  test('/connect page renders backend URL input + Connect button', async ({ page }) => {
    await page.goto('/connect')
    await expect(page.getByRole('heading', { name: 'Connect to Netwatch' })).toBeVisible({ timeout: 10000 })
    await expect(page.locator('input[type="url"]').first()).toBeVisible()
    await expect(page.getByRole('button', { name: /Connect/ })).toBeVisible()
  })

  test('/connect with empty URL → Connect button disabled', async ({ page }) => {
    await page.goto('/connect')
    await expect(page.locator('input[type="url"]').first()).toBeVisible({ timeout: 8000 })
    // Clear any pre-filled URL
    await page.locator('input[type="url"]').first().clear()
    await page.waitForTimeout(300)
    const btn = page.getByRole('button', { name: /Connect/ })
    // After BUG-1 fix: button is disabled when all URLs are empty
    await expect(btn).toBeDisabled()
  })

  test('/connect with unreachable backend → shows error', async ({ page }) => {
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill('http://127.0.0.1:19999')
    await page.getByRole('button', { name: /Connect/ }).click()
    // Should show error or stay on /connect
    await page.waitForTimeout(4000)
    // Should NOT have navigated to /setup or /login
    const url = page.url()
    expect(url).not.toMatch(/\/login$/)
    // Error message visible
    const hasError = await page.locator('text=/error|fail|cannot|refused/i').isVisible()
    expect(hasError || url.includes('/connect')).toBe(true)
  })

  test('/connect → valid backend → /setup or /login depending on cluster state', async ({ page }) => {
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    // Redirects to /setup if not set up yet, /login if already set up
    await expect(page).toHaveURL(/\/(setup|login)$/, { timeout: 10000 })
  })
})

test.describe('01 Auth — setup wizard', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('setup form: only reachable if setup not completed', async ({ page }) => {
    // Check cluster state — these tests only run on fresh cluster
    const statusRes = await fetch(`${BACKEND}/auth/status`)
    const status = await statusRes.json() as { setup_completed: boolean }
    if (status.setup_completed) {
      // Setup already done — verify /setup redirects away
      await page.goto('/connect')
      await page.locator('input[type="url"]').first().fill(BACKEND)
      await page.getByRole('button', { name: /Connect/ }).click()
      await expect(page).toHaveURL(/\/login$/, { timeout: 10000 })
      return
    }
    // Fresh cluster — /setup should be reachable
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })
    await expect(page.getByRole('heading', { name: /Initial Setup/ })).toBeVisible()
  })

  test('setup form: short password → HTML minlength validation blocks submit', async ({ page }) => {
    const statusRes = await fetch(`${BACKEND}/auth/status`)
    const status = await statusRes.json() as { setup_completed: boolean }
    if (status.setup_completed) { test.skip(); return }

    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })

    await page.locator('input[type="password"]').first().fill(SETUP_TOKEN)
    await page.locator('input[type="text"]').first().fill('admin')
    await page.locator('input[type="password"]').nth(1).fill('short')
    await page.getByRole('button', { name: /Create Admin User/ }).click()
    await page.waitForTimeout(1000)
    await expect(page).toHaveURL(/\/setup$/)
  })

  test('full setup wizard: creates admin + shows credentials (fresh cluster only)', async ({ page }) => {
    const statusRes = await fetch(`${BACKEND}/auth/status`)
    const status = await statusRes.json() as { setup_completed: boolean }
    if (status.setup_completed) { test.skip(); return }

    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })

    await page.locator('input[type="password"]').first().fill(SETUP_TOKEN)
    await page.locator('input[type="text"]').first().fill(ADMIN_USER)
    await page.locator('input[type="password"]').nth(1).fill(ADMIN_PASS)
    await page.getByRole('button', { name: /Create Admin User/ }).click()
    await expect(page.getByText('Setup Complete!')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText(ADMIN_USER)).toBeVisible()
    await page.getByRole('button', { name: /Go to Dashboard/ }).click()
    await expect(page).toHaveURL('/', { timeout: 10000 })
  })

  test('after setup: /setup redirects away (to /login or /connect)', async ({ page }) => {
    // In any state, /setup should never be the final destination
    await page.goto('/setup')
    await page.waitForTimeout(3000)
    // Should NOT stay on /setup — redirected to /login (has node) or /connect (no node)
    expect(page.url()).not.toMatch(/\/setup$/)
  })
})

test.describe('01 Auth — login', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('login: wrong password → error message', async ({ page }) => {
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/(login|setup)$/, { timeout: 10000 })

    if (page.url().includes('/setup')) {
      test.skip()
      return
    }

    await page.locator('input[type="text"]').fill(ADMIN_USER)
    await page.locator('input[type="password"]').fill('wrongpassword')
    await page.getByRole('button', { name: /Sign In/ }).click()

    // Error should appear
    await expect(page.locator('text=/invalid|wrong|fail|unauthorized/i')).toBeVisible({ timeout: 5000 })
    // Should stay on /login
    await expect(page).toHaveURL(/\/login$/)
  })

  test('login: empty username → native validation or error', async ({ page }) => {
    // Must seed nodesStore via /connect before /login is reachable
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/(login|setup)$/, { timeout: 10000 })
    if (!page.url().includes('/login')) { test.skip(); return }

    // Click sign in without filling fields
    await page.getByRole('button', { name: /Sign In/ }).click()
    await page.waitForTimeout(500)
    // Should still be on /login
    await expect(page).toHaveURL(/\/login$/)
  })

  test('login: correct credentials → dashboard', async ({ page }) => {
    await page.goto('/connect')
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()
    await expect(page).toHaveURL(/\/(login|setup)$/, { timeout: 10000 })
    if (!page.url().includes('/login')) { test.skip(); return }

    await page.locator('input[type="text"]').fill(ADMIN_USER)
    await page.locator('input[type="password"]').fill(ADMIN_PASS)
    await page.getByRole('button', { name: /Sign In/ }).click()
    await expect(page).toHaveURL('/', { timeout: 10000 })
    await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })
  })
})
