import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.CHRONOLENS_LATENCY_URL;
if (!baseURL || !/^http:\/\/127\.0\.0\.1:\d+$/.test(baseURL)) {
  throw new Error('Use npm run experiment:latency; the experiment owns a numeric loopback server.');
}
export default defineConfig({
  testDir: './experiments',
  testMatch: 'latency.spec.ts',
  workers: 1,
  retries: 0,
  timeout: 120_000,
  reporter: [['list']],
  use: { baseURL, ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1080 } },
});
