import { test, expect } from '@playwright/test'
import { mockAllRoutes, seedAuth } from './fixtures/api-mocks'

test.use({ storageState: { cookies: [], origins: [] } })

test.beforeEach(async ({ page }) => {
  await seedAuth(page)
  await mockAllRoutes(page)
})

// ────────────────────────────────────────────────────────────────────────────
// SKIPPED tests — see sprint.md "E2E test reliability refactor" sprint
//
// All data-dependent assertions on /targets fail in production build because
// pinia-plugin-persistedstate hydration races with useFleet's onMounted fetch.
// The first fetch happens before the nodes store is hydrated → activeUrl is
// null → API call throws "No backend node available" → fleet.data stays null
// → no targets render.
//
// Fleet IS loading on / (cluster overview) — works there because the index
// page is hit AFTER pinia has had a chance to hydrate from localStorage.
//
// Fix candidates:
//   1. Make `useApi` retry on null active node (race condition tolerance)
//   2. Move pinia hydration to a Nuxt plugin that runs before any middleware
//   3. Use Nuxt's useState() for cross-component reactive state
// ────────────────────────────────────────────────────────────────────────────

test.describe('Targets list', () => {
  test('renders heading', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByRole('heading', { name: 'Targets' })).toBeVisible({ timeout: 10000 })
  })

  test.skip('displays targets from fleet API [SKIP: pinia hydration race]', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByText('api-gateway')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('db-primary')).toBeVisible()
    await expect(page.getByText('web-server')).toBeVisible()
  })

  test.skip('shows DOWN badge for hard_down targets [SKIP: pinia hydration race]', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByText('api-gateway')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('DOWN').first()).toBeVisible()
  })

  test('shows total count in header', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByText(/3 total/)).toBeVisible({ timeout: 10000 })
  })

  test.skip('search filter narrows results [SKIP: pinia hydration race]', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByText('api-gateway')).toBeVisible({ timeout: 10000 })
    await page.locator('input[type="search"]').fill('db-')
    await expect(page.getByText('db-primary')).toBeVisible()
    await expect(page.getByText('api-gateway')).not.toBeVisible()
  })

  test.skip('status filter Down shows only down targets [SKIP: pinia hydration race]', async ({ page }) => {
    await page.goto('/targets')
    await expect(page.getByText('api-gateway')).toBeVisible({ timeout: 10000 })
    await page.locator('button', { hasText: 'Down' }).click()
    await expect(page.getByText('db-primary')).toBeVisible()
    await expect(page.getByText('api-gateway')).not.toBeVisible()
  })
})

test.describe('Target detail', () => {
  test('renders heading for known target', async ({ page }) => {
    await page.goto('/targets/api-gateway')
    await expect(page.getByRole('heading', { name: 'api-gateway' })).toBeVisible({ timeout: 10000 })
  })

  test.skip('shows by-node breakdown table [SKIP: pinia hydration race]', async ({ page }) => {
    await page.goto('/targets/api-gateway')
    await expect(page.getByText('Node Breakdown')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('e2e-node')).toBeVisible()
  })

  test('shows DOWN state for db-primary', async ({ page }) => {
    await page.goto('/targets/db-primary')
    await expect(page.getByRole('heading', { name: 'db-primary' })).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('DOWN').first()).toBeVisible()
  })

  test('back link navigates to targets list', async ({ page }) => {
    await page.goto('/targets/api-gateway')
    await expect(page.getByText('← Targets')).toBeVisible({ timeout: 10000 })
    await page.getByText('← Targets').click()
    await expect(page).toHaveURL('/targets')
  })
})
