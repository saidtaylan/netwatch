import { mockBackend } from './fixtures/mock-backend'

export default async function globalSetup() {
  // Guard: if already listening, skip (can happen with Playwright watch mode)
  if (mockBackend.listening) return

  await new Promise<void>((resolve, reject) => {
    mockBackend.listen(19240, () => {
      console.log('[e2e] Mock backend started on :19240')
      resolve()
    })
    mockBackend.once('error', (err: any) => {
      if (err.code === 'EADDRINUSE') { resolve(); return }  // already bound
      reject(err)
    })
  })
}

export async function globalTeardown() {
  if (!mockBackend.listening) return
  await new Promise<void>(resolve => mockBackend.close(() => resolve()))
}
