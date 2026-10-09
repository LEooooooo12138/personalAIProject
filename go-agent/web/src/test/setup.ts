import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// Component tests keep a deterministic fetch transport; browser E2E exercises
// the actual SharedWorker protocol and cross-tab scheduling.
vi.mock('../coordinator', () => ({
  coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options),
  setExpectedIdentity: () => {},
  setInvalidationHandler: () => {},
  openChatSocket: () => Promise.reject(new Error('mock WebSocket in the calling test')),
}))

afterEach(cleanup)
