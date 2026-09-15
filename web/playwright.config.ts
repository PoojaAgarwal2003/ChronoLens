import { defineConfig, devices } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';

process.env.CHRONOLENS_E2E_FORMAT = 'jsonl';
process.env.CHRONOLENS_E2E_DIR ??= fileURLToPath(new URL(`./node_modules/.cache/chronolens-e2e-${randomUUID()}`, import.meta.url));
process.env.CHRONOLENS_E2E_TOKEN ??= randomUUID();

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  globalTeardown: './e2e/cleanup.mjs',
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:8090',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'node e2e/server.mjs',
    url: 'http://127.0.0.1:8090/api/health',
    reuseExistingServer: false,
    timeout: 120_000,
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
  },
});
