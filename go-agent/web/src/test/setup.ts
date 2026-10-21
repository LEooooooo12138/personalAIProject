import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// jsdom lacks the browser dialog methods. Browser UI tests verify actual
// modality and keyboard focus; this adapter only synchronizes the open state.
if (!HTMLDialogElement.prototype.showModal) HTMLDialogElement.prototype.showModal = function () { this.open = true }
if (!HTMLDialogElement.prototype.close) HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new Event('close')) }

// Component tests keep a deterministic fetch transport; browser E2E exercises
// the actual SharedWorker protocol and cross-tab scheduling.
vi.mock('../coordinator', () => ({
  coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options),
  setExpectedIdentity: () => {},
  setInvalidationHandler: () => {},
  openChatSocket: () => Promise.reject(new Error('mock WebSocket in the calling test')),
}))

afterEach(cleanup)
