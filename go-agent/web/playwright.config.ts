import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Playwright forces color in child workers. Remove the conflicting hint only
// from this owned test process; the invoking shell and machine are unchanged.
delete process.env.NO_COLOR

const webRoot = path.dirname(fileURLToPath(import.meta.url))
const fixtureBinary = path.resolve(webRoot, '../.tmp', `consolefixture${process.platform === 'win32' ? '.exe' : ''}`)
const fixtureFlags = (process.env.CONSOLE_EXTENDED_CONTROLS === '1' ? ' -extended-controls' : '')
  + (process.env.CONSOLE_AUTOMATION_LIFECYCLE === '1' ? ' -automation-lifecycle' : '')
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
    command: `"${fixtureBinary}" -port ${port}${fixtureFlags}`,
    cwd: path.resolve(webRoot, '..'),
    env: { CONSOLE_FIXTURE_METRICS_PATH: path.resolve(webRoot, '../.tmp/chat-progress-metrics.jsonl') },
    url: `${baseURL}/app/`,
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
