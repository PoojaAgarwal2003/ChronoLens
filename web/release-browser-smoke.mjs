// Developer-only opt-in check; browsers and this script are not bundled.
import { chromium, expect } from '@playwright/test';

const base = process.argv[2];
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(base ?? '')) throw new Error('numeric loopback URL required');
const browser = await chromium.launch({ timeout: 20_000 });
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(15_000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(base, { timeout: 20_000 });
  await expect(page.getByTestId('match-count')).toHaveText('1,000');
  await expect(page.getByTestId('input-format')).toHaveText('SNAPSHOT');
  await expect(page.getByRole('heading', { name: 'Every event. A clearer view.' })).toBeVisible();
  await page.getByRole('button', { name: '1%', exact: true }).click();
  await expect(page.getByTestId('match-count')).toHaveText('10');
  await page.getByRole('button', { name: 'Compare engines' }).click();
  await expect(page.getByText('Equivalent aggregates across all three engines', { exact: false })).toBeVisible();
  await expect(page.getByTestId('comparison-table').locator('tbody tr')).toHaveCount(3);
  if (errors.length) throw new Error(errors.join('\n'));
  console.log('PASS: extracted-package UI, snapshot count, narrow query, engine comparison, no page errors');
} finally {
  await browser.close();
}
