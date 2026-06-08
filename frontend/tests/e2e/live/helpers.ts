/**
 * Shared helpers for live-backend Playwright tests.
 */
import { type Page, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'

export const BACKEND   = 'http://localhost:10241'
export const SETUP_TOKEN = 'demo-setup-token-shared-secret'
export const ADMIN_USER  = 'testadmin'
export const ADMIN_PASS  = 'AdminPass123!'

/** Navigate and wait until the page heading is visible. */
export async function gotoAndWait(page: Page, url: string, heading: string | RegExp, timeout = 12000) {
  await page.goto(url)
  await expect(page.getByRole('heading', { name: heading })).toBeVisible({ timeout })
}

/** Dismiss any open toast by waiting for it to disappear. */
export async function waitNoToast(page: Page) {
  const toast = page.locator('[class*="toast"], [data-toast]').first()
  if (await toast.isVisible()) {
    await toast.waitFor({ state: 'hidden', timeout: 5000 })
  }
}

/** Ensure the live auth state file directory exists. */
export function ensureAuthDir() {
  const dir = path.join(import.meta.dirname, '../../.auth')
  fs.mkdirSync(dir, { recursive: true })
}

/** POST to backend API directly (for test setup/teardown). */
export async function apiRequest(
  method: string,
  path: string,
  body?: object,
  token?: string,
): Promise<any> {
  const res = await fetch(`${BACKEND}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${path} → ${res.status}: ${text}`)
  return text ? JSON.parse(text) : null
}
