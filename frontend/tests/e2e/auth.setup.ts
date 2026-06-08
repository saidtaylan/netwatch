/**
 * auth.setup.ts — Verify login flow works end-to-end.
 * Creates .auth/state.json for any tests that still want shared auth state.
 */
import { test as setup, expect } from '@playwright/test'
import path from 'node:path'
import { mockAllRoutes } from './fixtures/api-mocks'

const AUTH_FILE = path.join(import.meta.dirname, '.auth/state.json')

setup('authenticate', async ({ page }) => {
  await mockAllRoutes(page)

  // Step 1: /connect — enter backend URL
  await page.goto('/connect')
  await expect(page.getByRole('heading', { name: 'Connect to Netwatch' })).toBeVisible({ timeout: 10000 })
  await page.locator('input[type="url"]').first().fill('http://localhost:19240')
  await page.getByRole('button', { name: /Connect/ }).click()

  // Step 2: /setup — first-run admin creation (mock starts with setup_completed:false)
  await expect(page).toHaveURL(/\/setup$/, { timeout: 10000 })
  await page.locator('input[type="password"]').first().fill('setup-token-xyz')
  await page.locator('input[type="text"]').first().fill('admin')
  await page.locator('input[type="password"]').nth(1).fill('strongpw1234')
  await page.getByRole('button', { name: /Create Admin User/ }).click()

  await expect(page.getByText('Setup Complete!')).toBeVisible({ timeout: 10000 })
  await page.getByRole('button', { name: /Go to Dashboard/ }).click()

  await expect(page).toHaveURL('/', { timeout: 10000 })
  await expect(page.getByRole('heading', { name: 'Cluster Overview' })).toBeVisible({ timeout: 10000 })

  await page.context().storageState({ path: AUTH_FILE })
})
