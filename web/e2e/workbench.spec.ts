import { expect, test, type Page } from '@playwright/test';
import { timeBounds, type Meta, type QueryResponse } from '../src/api';

async function ready(page: Page) {
  await page.goto('/');
  await expect(page.getByTestId('match-count')).toHaveText('10,000');
  await expect(page.getByTestId('input-format')).toHaveText(process.env.CHRONOLENS_E2E_FORMAT === 'snapshot' ? 'SNAPSHOT' : 'JSONL');
}
async function selectWindow(page: Page, name: string) {
  const response = page.waitForResponse(response => response.url().endsWith('/api/query') && response.status() === 200);
  await page.getByRole('button', { name, exact: true }).click();
  return (await response).json() as Promise<QueryResponse>;
}

test('timestamp bounds preserve int64 precision and the inclusive dataset maximum', () => {
  const meta = { min_timestamp_us: '9223372036854774807', max_timestamp_us: '9223372036854775807' };
  expect(timeBounds(meta, 0, 1000)).toEqual({ from_us: meta.min_timestamp_us, to_us: null });
  expect(timeBounds(meta, 0, 10)).toEqual({ from_us: meta.min_timestamp_us, to_us: '9223372036854774817' });
  expect(timeBounds(meta, 999, 1000)).toEqual({ from_us: '9223372036854775806', to_us: null });
});

test('real dataset renders exact charts, narrow indexed work, and engine-specific diagnostics', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await ready(page);
  await expect(page.getByRole('heading', { name: 'Every event. A clearer view.' })).toBeVisible();
  await expect(page.getByRole('img', { name: /Event timeline: 10,000 matches/ })).toBeVisible();
  const narrow = await selectWindow(page, '1%');
  expect(narrow.result.aggregate.count).toBe(100);
  expect(narrow.profile.timeline.reduce((sum, bin) => sum + bin.count, 0)).toBe(100);
  await expect(page.getByTestId('rows-skipped')).toHaveText('9,900');
  await expect(page.getByTestId('work-avoided')).toHaveText('99%');
  await page.getByLabel('Execution engine').selectOption('row');
  await expect(page.getByTestId('rows-examined')).toHaveText('10,000');
  await expect(page.getByTestId('rows-skipped')).toHaveText('0');
  await expect(page.getByTestId('match-count')).toHaveText('100');
  await page.getByLabel('Execution engine').selectOption('columnar');
  await expect(page.getByRole('status').filter({ hasText: 'Columnar scan' })).toBeVisible();
  await expect(page.getByTestId('rows-examined')).toHaveText('10,000');
  expect(errors).toEqual([]);
});

test('real service and status filters agree with the server, then compare equivalent warm batches', async ({ page, request }) => {
  const meta = await (await request.get('/api/meta')).json() as Meta;
  await ready(page);
  await page.getByLabel('Service', { exact: true }).selectOption(meta.services[0]);
  await page.getByLabel('HTTP status').selectOption('500');
  const result = await (await request.post('/api/query', { data: {
    engine: 'indexed', from_us: meta.min_timestamp_us, to_us: null, service: meta.services[0], status: 500, buckets: 100,
  } })).json() as QueryResponse;
  await expect(page.getByTestId('match-count')).toHaveText(result.result.aggregate.count.toLocaleString('en-US'));
  await expect(page.getByTestId('error-count')).toHaveText(result.result.aggregate.error_count.toLocaleString('en-US'));
  await page.getByRole('button', { name: 'Compare engines' }).click();
  await expect(page.getByText('Equivalent aggregates across all three engines', { exact: false })).toBeVisible();
  await expect(page.getByTestId('comparison-table').locator('tbody tr')).toHaveCount(3);
  await expect(page.getByText('Warm batch means, not p95 or end-to-end latency.', { exact: false })).toBeVisible();
  await page.getByLabel('HTTP status').selectOption('0');
  await expect(page.getByTestId('comparison-table')).toHaveCount(0);
});

test('a real empty intersection is explained and resettable', async ({ page, request }) => {
  const meta = await (await request.get('/api/meta')).json() as Meta;
  let service = '';
  for (const candidate of meta.services) {
    const result = await (await request.post('/api/query', { data: {
      engine: 'indexed', ...timeBounds(meta, 0, 1), service: candidate, status: 500, buckets: 100,
    } })).json() as QueryResponse;
    if (result.result.aggregate.count === 0) { service = candidate; break; }
  }
  expect(service).not.toBe('');
  await ready(page);
  await page.getByRole('button', { name: '0.1%', exact: true }).click();
  await page.getByLabel('Service', { exact: true }).selectOption(service);
  await page.getByLabel('HTTP status').selectOption('500');
  await expect(page.getByText('No matching events', { exact: true })).toBeVisible();
  await expect(page.getByTestId('match-count')).toHaveText('0');
  await expect(page.getByTestId('mean-duration')).toHaveText('No observations');
  await page.getByRole('button', { name: 'Reset filters' }).click();
  await expect(page.getByTestId('match-count')).toHaveText('10,000');
});

test('network errors never leave old results looking current and retry recovers', async ({ page }) => {
  await ready(page);
  await page.route('**/api/query', route => route.abort('failed'));
  await page.getByRole('button', { name: '10%', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Query unavailable');
  await expect(page.getByTestId('match-count')).toHaveText('—');
  await page.unroute('**/api/query');
  await page.getByRole('button', { name: 'Retry query' }).click();
  await expect(page.getByTestId('match-count')).toHaveText('1,000');
});

test('late query responses cannot overwrite a newer filter selection', async ({ page }) => {
  await ready(page);
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let intercepted!: () => void;
  const started = new Promise<void>(resolve => { intercepted = resolve; });
  let delivered!: () => void;
  let deliveryFailed!: (error: unknown) => void;
  const delivery = new Promise<void>((resolve, reject) => { delivered = resolve; deliveryFailed = reject; });
  await page.route('**/api/query', async route => {
    const body = route.request().postDataJSON();
    if (body.engine === 'row') {
      const response = await route.fetch();
      intercepted();
      await gate;
      await route.fulfill({ response }).then(delivered, deliveryFailed);
    } else await route.continue();
  });
  await page.getByLabel('Execution engine').selectOption('row');
  await started;
  await page.getByRole('button', { name: '1%', exact: true }).click();
  await page.getByLabel('Execution engine').selectOption('indexed');
  await expect(page.getByTestId('match-count')).toHaveText('100');
  release();
  await delivery;
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => resolve())));
  await expect(page.getByTestId('rows-examined')).toHaveText('100');
  await expect(page.getByTestId('rows-skipped')).toHaveText('9,900');
});

test('metadata failures and null clock measurements have explicit labels', async ({ page }) => {
  await page.route('**/api/meta', route => route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: { code: 'internal', message: 'Metadata test unavailable' } }) }));
  await page.goto('/');
  await expect(page.getByRole('alert')).toContainText('Metadata test unavailable');
  await page.unroute('**/api/meta');
  await page.route('**/api/query', async route => {
    const response = await route.fetch();
    const body = await response.json() as QueryResponse;
    body.timing = { query_ms: null, profile_ms: null, server_work_ms: null };
    await route.fulfill({ response, json: body });
  });
  await page.getByRole('button', { name: 'Retry connection' }).click();
  await expect(page.getByTestId('match-count')).toHaveText('10,000');
  await expect(page.getByText('Below clock resolution', { exact: true })).toHaveCount(3);
});

test('empty dataset metadata does not invent timestamps or issue queries', async ({ page, request }) => {
  const meta = await (await request.get('/api/meta')).json() as Meta;
  let queries = 0;
  page.on('request', request => { if (request.url().endsWith('/api/query')) queries++; });
  await page.route('**/api/meta', route => route.fulfill({
    contentType: 'application/json',
    json: { ...meta, dataset: 'empty.jsonl', rows: 0, service_count: 0, services: [], statuses: [], min_timestamp_us: null, max_timestamp_us: null },
  }));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'No events loaded' })).toBeVisible();
  await expect(page.getByRole('slider')).toHaveCount(0);
  await expect(page.getByText(/1970-01-01/)).toHaveCount(0);
  expect(queries).toBe(0);
});

test('keyboard controls, reduced motion, responsive layout, and real screenshot', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await ready(page);
  await page.keyboard.press('Tab');
  await expect(page.getByRole('link', { name: 'Skip to workbench' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('main')).toBeFocused();
  const start = page.getByRole('slider', { name: 'Range start' });
  await start.focus();
  await page.keyboard.press('ArrowRight');
  await expect(start).toHaveValue('1');
  await page.getByRole('button', { name: '1%', exact: true }).click();
  await expect(page.getByTestId('match-count')).toHaveText('100');
  await page.getByRole('slider', { name: 'Range start' }).fill('999');
  await expect(start).toHaveValue('9');
  await page.getByRole('button', { name: '100%', exact: true }).click();
  await expect(page.getByTestId('match-count')).toHaveText('10,000');
  await page.getByRole('button', { name: 'Compare engines' }).click();
  await expect(page.getByTestId('comparison-table')).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.getByRole('heading', { name: 'Every event. A clearer view.' }).click();
  await page.screenshot({ path: 'test-results/chronolens-workbench.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByLabel('Execution engine')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/chronolens-mobile.png', fullPage: true });
});

test('explicit presets dispatch with a frozen clock and repeated presets are no-ops', async ({ page }) => {
  await ready(page);
  await page.clock.install();
  await page.clock.pauseAt(new Date());
  let queries = 0;
  page.on('request', request => { if (request.url().endsWith('/api/query')) queries++; });
  const response = page.waitForResponse(response => response.url().endsWith('/api/query'));
  await page.getByRole('button', { name: '10%', exact: true }).click();
  expect((await (await response).json() as QueryResponse).result.aggregate.count).toBe(1000);
  await expect(page.getByTestId('match-count')).toHaveText('1,000');
  await page.getByRole('button', { name: '10%', exact: true }).click();
  await page.clock.runFor(1000);
  expect(queries).toBe(1);
});

test('slider changes coalesce for 100ms after the final keyboard or drag input', async ({ page, request }) => {
  const meta = await (await request.get('/api/meta')).json() as Meta;
  await ready(page);
  await page.clock.install();
  await page.clock.pauseAt(new Date());
  const queries: unknown[] = [];
  page.on('request', request => { if (request.url().endsWith('/api/query')) queries.push(request.postDataJSON()); });
  const end = page.getByRole('slider', { name: 'Range end' });
  await end.fill('900');
  await expect(page.getByTestId('match-count')).toHaveText('—');
  await page.clock.runFor(99);
  expect(queries).toHaveLength(0);
  await end.fill('800');
  await page.clock.runFor(99);
  expect(queries).toHaveLength(0);
  await end.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(end).toHaveValue('799');
  await page.clock.runFor(99);
  expect(queries).toHaveLength(0);
  const dispatched = page.waitForRequest(request => request.url().endsWith('/api/query'));
  await page.clock.runFor(1);
  await dispatched;
  expect(queries).toEqual([{ engine: 'indexed', ...timeBounds(meta, 0, 799), service: '', status: 0, buckets: 100 }]);
  await expect(page.getByTestId('match-count')).toHaveText('7,990');
});

test('preset and discrete filters supersede a pending slider without a delayed stale query', async ({ page, request }) => {
  const meta = await (await request.get('/api/meta')).json() as Meta;
  await ready(page);
  await page.clock.install();
  await page.clock.pauseAt(new Date());
  const queries: unknown[] = [];
  page.on('request', request => { if (request.url().endsWith('/api/query')) queries.push(request.postDataJSON()); });
  await page.getByRole('slider', { name: 'Range end' }).fill('100');
  await expect(page.getByTestId('match-count')).toHaveText('—');
  await page.clock.runFor(99);
  // Same numeric bounds as the pending slider still flush immediately.
  await selectWindow(page, '10%');
  await page.getByRole('slider', { name: 'Range start' }).fill('1');
  await expect(page.getByTestId('match-count')).toHaveText('—');
  const response = page.waitForResponse(response => response.url().endsWith('/api/query'));
  await page.getByLabel('Service', { exact: true }).selectOption(meta.services[0]);
  const body = await (await response).json() as QueryResponse;
  await expect(page.getByTestId('match-count')).toHaveText(body.result.aggregate.count.toLocaleString('en-US'));
  await page.clock.runFor(1000);
  expect(queries).toEqual([
    { engine: 'indexed', ...timeBounds(meta, 0, 100), service: '', status: 0, buckets: 100 },
    { engine: 'indexed', ...timeBounds(meta, 1, 100), service: meta.services[0], status: 0, buckets: 100 },
  ]);
});
