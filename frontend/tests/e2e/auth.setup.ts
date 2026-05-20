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

  await page.goto('/setup')
  await expect(page.getByText('Connect to Backend')).toBeVisible({ timeout: 10000 })

  await page.locator('input[type="url"]').first().fill('http://localhost:19240')
  await page.locator('input[type="password"]').fill('test-token')
  await page.locator('button[type="submit"]').click()

  await expect(page).toHaveURL('/', { timeout: 10000 })
  await expect(page.getByText('Cluster Overview')).toBeVisible({ timeout: 10000 })

  await page.context().storageState({ path: AUTH_FILE })
})
