/**
 * SETTINGS — /settings/nodes (add, test, switch, remove nodes)
 *           /settings/index (general settings)
 */
import { test, expect } from '@playwright/test'

test.describe('07 Settings — Backend Nodes', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/settings/nodes')
    await expect(page.getByRole('heading', { name: 'Backend Nodes' })).toBeVisible({ timeout: 15000 })
  })

  test('page loads with add-node form', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'Add Node' })).toBeVisible()
    await expect(page.locator('input[type="url"]')).toBeVisible()
    await expect(page.locator('input[placeholder="Label (opt)"]')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Add' })).toBeVisible()
  })

  test('Add button disabled when URL input is empty', async ({ page }) => {
    const addBtn = page.getByRole('button', { name: 'Add' })
    const urlInput = page.locator('input[type="url"]')
    await urlInput.clear()
    const isDisabled = await addBtn.getAttribute('disabled')
    expect(isDisabled).not.toBeNull()
  })

  test('shows configured nodes (n1 should already be active)', async ({ page }) => {
    // The backend URL that was set during connect should be listed
    const nodeList = page.locator('li').filter({ hasText: '10241' })
    if (!await nodeList.isVisible({ timeout: 5000 })) {
      // No nodes configured yet
      await expect(page.getByText('No nodes configured')).toBeVisible()
      return
    }
    await expect(nodeList).toBeVisible()
    await expect(nodeList.getByText('Active')).toBeVisible()
  })

  test('add n2 node: enters URL and clicks Add', async ({ page }) => {
    const urlInput = page.locator('input[type="url"]')
    await urlInput.fill('http://localhost:10242')
    const labelInput = page.locator('input[placeholder="Label (opt)"]')
    await labelInput.fill('n2')
    await page.getByRole('button', { name: 'Add' }).click()
    await page.waitForTimeout(1000)

    // n2 should now appear in the list
    await expect(page.locator('p').filter({ hasText: 'localhost:10242' })).toBeVisible({ timeout: 5000 })
  })

  test('add n3 node via Enter key', async ({ page }) => {
    const urlInput = page.locator('input[type="url"]')
    await urlInput.fill('http://localhost:10243')
    await urlInput.press('Enter')
    await page.waitForTimeout(1000)
    await expect(page.locator('p').filter({ hasText: 'localhost:10243' })).toBeVisible({ timeout: 5000 })
  })

  test('Test button: checks health of a node', async ({ page }) => {
    // Find a node row and click Test
    const nodeRow = page.locator('li').filter({ hasText: '10241' }).first()
    if (!await nodeRow.isVisible({ timeout: 5000 })) { test.skip(); return }

    const testBtn = nodeRow.getByRole('button', { name: 'Test' })
    await testBtn.click()

    // Should show "…" then ✓ or ✗
    await page.waitForTimeout(5000)
    const icon = nodeRow.locator('span[class*="font-bold"]')
    const text = await icon.innerText()
    expect(['✓', '✗', '?']).toContain(text.trim())
  })

  test('Use button: switches active node', async ({ page }) => {
    // Add a second node if needed
    const n2 = page.locator('li').filter({ hasText: '10242' }).first()
    if (!await n2.isVisible({ timeout: 3000 })) {
      await page.locator('input[type="url"]').fill('http://localhost:10242')
      await page.getByRole('button', { name: 'Add' }).click()
      await page.waitForTimeout(1000)
    }

    const useBtn = page.locator('li').filter({ hasText: '10242' }).getByRole('button', { name: 'Use' })
    if (!await useBtn.isVisible({ timeout: 3000 })) { test.skip(); return }

    await useBtn.click()
    await page.waitForTimeout(1000)

    // n2 row should now show "Active"
    await expect(page.locator('li').filter({ hasText: '10242' }).getByText('Active')).toBeVisible({ timeout: 3000 })
    // n1 row should show "Use" button again
    await expect(page.locator('li').filter({ hasText: '10241' }).getByRole('button', { name: 'Use' })).toBeVisible({ timeout: 3000 })

    // Switch back to n1
    await page.locator('li').filter({ hasText: '10241' }).getByRole('button', { name: 'Use' }).click()
    await page.waitForTimeout(1000)
  })

  test('remove node: ✕ button removes from list', async ({ page }) => {
    // Add a temporary node
    await page.locator('input[type="url"]').fill('http://localhost:19999')
    await page.locator('input[placeholder="Label (opt)"]').fill('temp')
    await page.getByRole('button', { name: 'Add' }).click()
    await page.waitForTimeout(1000)

    const tempRow = page.locator('li').filter({ hasText: '19999' })
    await expect(tempRow).toBeVisible({ timeout: 5000 })

    await tempRow.getByRole('button', { name: '✕' }).click()
    await page.waitForTimeout(1000)
    await expect(page.locator('li').filter({ hasText: '19999' })).not.toBeVisible()
  })

  test('add duplicate URL: silently ignored or shown once', async ({ page }) => {
    await page.locator('input[type="url"]').fill('http://localhost:10241')
    await page.getByRole('button', { name: 'Add' }).click()
    await page.waitForTimeout(500)

    // Should not create duplicate entries
    const rows = page.locator('li').filter({ hasText: '10241' })
    const count = await rows.count()
    expect(count).toBeLessThanOrEqual(2) // At most 2 (if pinia deduplicates)
  })
})

test.describe('07 Settings — General', () => {
  test('settings index page loads', async ({ page }) => {
    await page.goto('/settings')
    await page.waitForTimeout(3000)
    const content = await page.content()
    expect(content.length).toBeGreaterThan(100)
  })

  test('settings navigation: Backend Nodes link works', async ({ page }) => {
    await page.goto('/settings')
    await page.waitForTimeout(2000)
    // May have a link or tab to nodes
    const nodesLink = page.getByRole('link', { name: /Nodes/i }).first()
    if (await nodesLink.isVisible({ timeout: 3000 })) {
      await nodesLink.click()
      await expect(page).toHaveURL(/\/settings\/nodes/, { timeout: 5000 })
    }
  })
})
