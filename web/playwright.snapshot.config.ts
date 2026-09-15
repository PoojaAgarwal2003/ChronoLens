import { defineConfig, devices } from '@playwright/test';
import base from './playwright.config';

process.env.CHRONOLENS_E2E_FORMAT = 'snapshot';

export default defineConfig({
  ...base,
  projects: [{ name: 'chromium-snapshot', use: { ...devices['Desktop Chrome'] } }],
});
