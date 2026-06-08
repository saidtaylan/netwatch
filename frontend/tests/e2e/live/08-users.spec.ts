/**
 * USERS (/users) — Admin-only CRUD tests
 */
import { test, expect } from '@playwright/test'
import { BACKEND, ADMIN_USER, ADMIN_PASS, apiRequest } from './helpers'

const TEST_USERNAME = 'e2e-test-viewer'
const TEST_USERNAME_2 = 'e2e-test-operator'

test.describe('08 Users — Admin Management', () => {
  // Clean up any leftover test users from previous runs before the suite starts
  test.beforeAll(async () => {
    try {
      // Use direct fetch via apiRequest helper (bypasses Playwright baseURL)
      const { token } = await apiRequest('POST', '/auth/login', { username: ADMIN_USER, password: ADMIN_PASS })
      const users = await apiRequest('GET', '/users', undefined, token) as { username: string; id: string }[]
      for (const user of users) {
        if (user.username === TEST_USERNAME || user.username === TEST_USERNAME_2) {
          await apiRequest('DELETE', `/users/${user.id}`, undefined, token)
          console.log(`[08] beforeAll: deleted user ${user.username}`)
        }
      }
    } catch (e) {
      console.log('[08] beforeAll cleanup error (non-fatal):', e)
    }
  })

  test.beforeEach(async ({ page }) => {
    await page.goto('/users')
    await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible({ timeout: 15000 })
  })

  test('page loads with header and description', async ({ page }) => {
    await expect(page.getByText('Manage admin, operator and viewer accounts.')).toBeVisible()
  })

  test('+ Add User button is visible', async ({ page }) => {
    await expect(page.getByRole('button', { name: /Add User/ })).toBeVisible()
  })

  test('users table shows column headers', async ({ page }) => {
    await expect(page.getByText('Username')).toBeVisible()
    await expect(page.getByText('Role')).toBeVisible()
    await expect(page.getByText('Status')).toBeVisible()
    await expect(page.getByText('Actions')).toBeVisible()
  })

  test('admin user appears in the list', async ({ page }) => {
    // testadmin was created during setup
    await expect(page.getByText('testadmin')).toBeVisible({ timeout: 8000 })
    // Role badge should show "admin"
    await expect(page.locator('span').filter({ hasText: /^admin$/ }).first()).toBeVisible()
  })

  test('self (admin) does not have a Delete button', async ({ page }) => {
    const adminRow = page.locator('tr').filter({ hasText: 'testadmin' })
    await expect(adminRow).toBeVisible()
    // No Delete button in the admin's own row
    const deleteInOwnRow = adminRow.getByRole('button', { name: 'Delete' })
    await expect(deleteInOwnRow).not.toBeVisible()
    // Edit button should be visible
    await expect(adminRow.getByRole('button', { name: 'Edit' })).toBeVisible()
  })

  test('+ Add User: opens modal with form fields', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await expect(page.getByRole('dialog', { name: /Add User/ }).or(page.locator('[class*="fixed inset-0"]'))).toBeVisible({ timeout: 5000 })
    await expect(page.locator('label', { hasText: 'Username' })).toBeVisible()
    await expect(page.locator('label', { hasText: 'Password' })).toBeVisible()
    await expect(page.locator('label', { hasText: 'Role' })).toBeVisible()
    await expect(page.locator('label', { hasText: 'Display Name' })).toBeVisible()
    await expect(page.locator('input[type="checkbox"]')).toBeVisible()
  })

  test('+ Add User: Cancel closes modal', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await page.waitForTimeout(500)
    await page.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.locator('[class*="fixed inset-0"]')).not.toBeVisible()
  })

  test('+ Add User: click backdrop closes modal', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await page.waitForTimeout(500)
    await page.locator('[class*="fixed inset-0"]').click({ position: { x: 10, y: 10 } })
    await page.waitForTimeout(500)
    await expect(page.locator('[class*="fixed inset-0"]')).not.toBeVisible()
  })

  test('+ Add User: short password → form validation prevents submit', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await page.locator('input[type="text"]').first().fill('shortpwtest')
    await page.locator('input[type="password"]').fill('short')  // < 8 chars
    await page.getByRole('button', { name: /Create/ }).click()
    await page.waitForTimeout(500)
    // Modal should still be open (minlength=8)
    await expect(page.locator('[class*="fixed inset-0"]')).toBeVisible()
    await page.getByRole('button', { name: 'Cancel' }).click()
  })

  test('+ Add User: empty username → cannot submit', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await page.locator('input[type="password"]').fill('validpassword123')
    await page.getByRole('button', { name: /Create/ }).click()
    await page.waitForTimeout(500)
    // required field on username — form should not submit
    await expect(page.locator('[class*="fixed inset-0"]')).toBeVisible()
    await page.getByRole('button', { name: 'Cancel' }).click()
  })

  test('create viewer user: happy path', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await page.waitForTimeout(300)

    // Fill form
    await page.locator('input[type="text"]').first().fill(TEST_USERNAME)
    await page.locator('input[type="password"]').fill('ViewerPass123!')
    await page.locator('select').selectOption('viewer')
    await page.locator('input[type="text"]').last().fill('E2E Viewer')

    await page.getByRole('button', { name: /Create/ }).click()

    // Toast: "User created."
    await expect(page.locator('text=User created.')).toBeVisible({ timeout: 8000 })
    await page.waitForTimeout(1000)

    // User appears in table
    await expect(page.locator('tr').filter({ hasText: TEST_USERNAME })).toBeVisible({ timeout: 8000 })
  })

  test('create operator user: role badge shows "operator"', async ({ page }) => {
    await page.getByRole('button', { name: /Add User/ }).click()
    await page.waitForTimeout(300)

    await page.locator('input[type="text"]').first().fill(TEST_USERNAME_2)
    await page.locator('input[type="password"]').fill('OperatorPass123!')
    await page.locator('select').selectOption('operator')

    await page.getByRole('button', { name: /Create/ }).click()
    await expect(page.locator('text=User created.')).toBeVisible({ timeout: 8000 })
    await page.waitForTimeout(1000)

    const opRow = page.locator('tr').filter({ hasText: TEST_USERNAME_2 })
    await expect(opRow).toBeVisible({ timeout: 5000 })
    await expect(opRow.locator('span').filter({ hasText: 'operator' })).toBeVisible()
  })

  test('role badges: admin=red, operator=amber, viewer=gray', async ({ page }) => {
    await page.waitForTimeout(2000)

    const adminBadge    = page.locator('span').filter({ hasText: /^admin$/ }).first()
    const operatorBadge = page.locator('span').filter({ hasText: /^operator$/ }).first()
    const viewerBadge   = page.locator('span').filter({ hasText: /^viewer$/ }).first()

    if (await adminBadge.isVisible()) {
      const cls = await adminBadge.getAttribute('class')
      expect(cls).toContain('red')
    }
    if (await operatorBadge.isVisible()) {
      const cls = await operatorBadge.getAttribute('class')
      expect(cls).toContain('amber')
    }
    if (await viewerBadge.isVisible()) {
      const cls = await viewerBadge.getAttribute('class')
      expect(cls).toContain('gray')
    }
  })

  test('edit user: opens pre-filled edit modal', async ({ page }) => {
    const viewerRow = page.locator('tr').filter({ hasText: TEST_USERNAME })
    if (!await viewerRow.isVisible({ timeout: 5000 })) { test.skip(); return }

    await viewerRow.getByRole('button', { name: 'Edit' }).click()
    await page.waitForTimeout(300)

    await expect(page.getByText('Edit User')).toBeVisible()

    // Username should be pre-filled
    const usernameInput = page.locator('input[type="text"]').first()
    const val = await usernameInput.inputValue()
    expect(val).toBe(TEST_USERNAME)

    // Password should be empty (change only)
    const pwInput = page.locator('input[type="password"]')
    const pwVal = await pwInput.inputValue()
    expect(pwVal).toBe('')
  })

  test('edit user: update display name', async ({ page }) => {
    const viewerRow = page.locator('tr').filter({ hasText: TEST_USERNAME })
    if (!await viewerRow.isVisible({ timeout: 5000 })) { test.skip(); return }

    await viewerRow.getByRole('button', { name: 'Edit' }).click()
    await page.waitForTimeout(300)

    const displayInput = page.locator('input[type="text"]').last()
    await displayInput.clear()
    await displayInput.fill('E2E Viewer Updated')

    await page.getByRole('button', { name: /Save/ }).click()
    await expect(page.locator('text=User updated.')).toBeVisible({ timeout: 8000 })

    // Updated display name should appear
    await expect(page.locator('td').filter({ hasText: 'E2E Viewer Updated' })).toBeVisible({ timeout: 5000 })
  })

  test('edit user: change role from viewer to operator', async ({ page }) => {
    const viewerRow = page.locator('tr').filter({ hasText: TEST_USERNAME })
    if (!await viewerRow.isVisible({ timeout: 5000 })) { test.skip(); return }

    await viewerRow.getByRole('button', { name: 'Edit' }).click()
    await page.waitForTimeout(300)

    await page.locator('select').selectOption('operator')
    await page.getByRole('button', { name: /Save/ }).click()
    await expect(page.locator('text=User updated.')).toBeVisible({ timeout: 8000 })
    await page.waitForTimeout(1000)

    // Role badge should now show "operator"
    const updatedRow = page.locator('tr').filter({ hasText: TEST_USERNAME })
    await expect(updatedRow.locator('span').filter({ hasText: 'operator' })).toBeVisible({ timeout: 5000 })
  })

  test('delete user: confirm dialog + user removed', async ({ page }) => {
    const testRow = page.locator('tr').filter({ hasText: TEST_USERNAME })
    if (!await testRow.isVisible({ timeout: 5000 })) { test.skip(); return }

    await testRow.getByRole('button', { name: 'Delete' }).click()

    // Confirm dialog
    await expect(page.getByText('Delete User')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(`Are you sure you want to delete`)).toBeVisible()
    await expect(page.getByText(TEST_USERNAME)).toBeVisible()

    await page.getByRole('button', { name: 'Delete' }).last().click()

    await expect(page.locator('text=User deleted.')).toBeVisible({ timeout: 8000 })
    await page.waitForTimeout(1000)
    await expect(page.locator('tr').filter({ hasText: TEST_USERNAME })).not.toBeVisible()
  })

  test('delete user: cancel on confirm dialog keeps user', async ({ page }) => {
    const testRow = page.locator('tr').filter({ hasText: TEST_USERNAME_2 })
    if (!await testRow.isVisible({ timeout: 5000 })) { test.skip(); return }

    await testRow.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByText('Delete User')).toBeVisible()

    // Cancel: don't delete
    await page.getByRole('button', { name: 'Cancel' }).click()
    await page.waitForTimeout(500)
    await expect(page.locator('[class*="fixed inset-0"]')).not.toBeVisible()

    // User should still exist
    await expect(page.locator('tr').filter({ hasText: TEST_USERNAME_2 })).toBeVisible()
  })

  test('cleanup: delete test operator user', async ({ page }) => {
    await page.waitForTimeout(1000)
    const testRow = page.locator('tr').filter({ hasText: TEST_USERNAME_2 })
    if (!await testRow.isVisible({ timeout: 5000 })) {
      // Already deleted by a previous test or test run — that's fine
      return
    }

    await testRow.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByText('Delete User')).toBeVisible({ timeout: 5000 })
    await page.getByRole('button', { name: 'Delete' }).last().click()
    // Toast may already be gone if fast — just verify user is removed from list
    await page.waitForTimeout(2000)
    await expect(page.locator('tr').filter({ hasText: TEST_USERNAME_2 })).not.toBeVisible({ timeout: 5000 })
  })
})
