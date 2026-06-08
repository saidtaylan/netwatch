/**
 * Live-backend auth setup.
 * Runs once before all live tests. Creates admin user (if needed) and saves
 * browser storage state so subsequent specs start authenticated.
 */
import { test as setup, expect } from '@playwright/test'
import { BACKEND, SETUP_TOKEN, ADMIN_USER, ADMIN_PASS, ensureAuthDir } from './helpers'

const AUTH_FILE = './tests/e2e/.auth/live-state.json'

setup('live auth setup', async ({ page }) => {
  ensureAuthDir()

  // Check backend status
  const statusRes = await fetch(`${BACKEND}/auth/status`)
  const status = await statusRes.json() as { setup_completed: boolean }

  if (!status.setup_completed) {
    // ── First-run wizard ────────────────────────────────────────────────
    await page.goto('/connect')
    await expect(page.getByRole('heading', { name: 'Connect to Netwatch' })).toBeVisible({ timeout: 15000 })

    // Enter backend URL
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()

    // Should land on /setup since setup_completed = false
    await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })
    await expect(page.getByRole('heading', { name: /Initial Setup/ })).toBeVisible()

    // Fill setup form
    await page.locator('input[type="password"]').first().fill(SETUP_TOKEN)
    await page.locator('input[type="text"]').first().fill(ADMIN_USER)
    await page.locator('input[type="password"]').nth(1).fill(ADMIN_PASS)
    await page.getByRole('button', { name: /Create Admin User/ }).click()

    // Verify success screen
    await expect(page.getByText('Setup Complete!')).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: /Go to Dashboard/ }).click()

    await expect(page).toHaveURL('/', { timeout: 10000 })

  } else {
    // ── Normal login ────────────────────────────────────────────────────
    // Must go through /connect first to seed nodesStore in localStorage;
    // navigating directly to /login with no node configured redirects to /connect.
    await page.goto('/connect')
    await expect(page.getByRole('heading', { name: 'Connect to Netwatch' })).toBeVisible({ timeout: 15000 })
    await page.locator('input[type="url"]').first().fill(BACKEND)
    await page.getByRole('button', { name: /Connect/ }).click()

    // Now on /login
    await expect(page).toHaveURL(/\/login$/, { timeout: 10000 })
    await expect(page.locator('input[type="text"]')).toBeVisible({ timeout: 10000 })
    await page.locator('input[type="text"]').fill(ADMIN_USER)
    await page.locator('input[type="password"]').fill(ADMIN_PASS)
    await page.getByRole('button', { name: /Sign In/ }).click()
    await expect(page).toHaveURL('/', { timeout: 10000 })
  }

  // Verify dashboard loaded
  await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })

  // Save auth state
  await page.context().storageState({ path: AUTH_FILE })
  console.log(`[live-setup] Auth state saved: ${AUTH_FILE}`)
})
