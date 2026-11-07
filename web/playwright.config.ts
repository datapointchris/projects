import { defineConfig, devices } from '@playwright/test'
import process from 'node:process'

// The smoke runs against the dev stack, which `task web:smoke` starts and stops:
// Caddy serving the build, the API, Postgres, and a stand-in identity provider.
const baseURL = process.env.PROJECTS_WEB_STACK_URL || 'http://localhost:8096'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  workers: 1,
  reporter: [['list']],
  timeout: 30000,
  expect: { timeout: 10000 },
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
