import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = path.dirname(fileURLToPath(import.meta.url))
const port = 18081
const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 12_000 },
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    ...devices['Desktop Chrome'],
    browserName: 'chromium',
    channel: process.env.CONSOLE_BROWSER_CHANNEL || undefined,
    baseURL,
    trace: 'retain-on-failure',
  },
  webServer: {
    command: `go run ./tests/consolefixture -port ${port}`,
    cwd: path.resolve(webRoot, '..'),
    url: `${baseURL}/app/`,
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
