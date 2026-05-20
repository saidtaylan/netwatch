import { vi } from 'vitest'

// Stub $fetch globally — individual tests override as needed
vi.stubGlobal('$fetch', vi.fn())

// Stub navigateTo (used by auth composable + middleware)
vi.stubGlobal('navigateTo', vi.fn())
