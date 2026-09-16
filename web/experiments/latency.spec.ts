import { expect, test } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
import os from 'node:os';
import type { Meta, QueryResponse } from '../src/api';

test('opt-in individual click-to-visible-commit samples', async ({ page, request, browser }) => {
  const meta = await (await request.get('/api/meta')).json() as Meta;
  expect(meta.rows).toBe(1_000_000);
  const samples: unknown[] = [];
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    const state = { dispatch: 0 };
    Object.assign(window, { latencyState: state });
    const original = window.fetch.bind(window);
    window.fetch = (...args) => {
      if (String(args[0]).endsWith('/api/query')) state.dispatch = performance.now();
      return original(...args);
    };
  });
  await page.goto('/');
  await expect(page.getByTestId('match-count')).toHaveText('1,000,000');
  // Alternating ranges always triggers new work. Four initial clicks warm the UI.
  try {
    for (let i = 0; i < 44; i++) {
      const narrow = i % 2 === 0;
      const name = narrow ? '10%' : '100%';
      const expected = narrow ? '100,000' : '1,000,000';
      await page.evaluate(({ name, expected }) => {
        performance.clearResourceTimings();
        const button = [...document.querySelectorAll('button')].find(b => b.textContent === name)!;
        const state = window as unknown as {
          latencyState: { dispatch: number };
          latencySample: Promise<Record<string, number>>;
        };
        state.latencySample = new Promise((resolve, reject) => {
          const timeout = setTimeout(() => { observer.disconnect(); reject(new Error('visible commit timed out')); }, 10_000);
          let click = 0;
          const observer = new MutationObserver(() => {
            const result = document.querySelector('.results');
            const count = document.querySelector('[data-testid="match-count"]');
            const timeline = result?.querySelector('svg[role="img"]');
            const countBounds = count?.getBoundingClientRect();
            if (!click || result?.getAttribute('aria-busy') !== 'false' || count?.textContent !== expected || !timeline ||
                !countBounds || countBounds.width === 0 || countBounds.height === 0 ||
                countBounds.top < 0 || countBounds.bottom > innerHeight || countBounds.left < 0 ||
                countBounds.right > innerWidth || getComputedStyle(count).visibility !== 'visible') return;
            observer.disconnect();
            const domCommit = performance.now();
            requestAnimationFrame(() => requestAnimationFrame(() => {
              clearTimeout(timeout);
              const visible = performance.now();
              const entry = performance.getEntriesByType('resource').filter(e => e.name.endsWith('/api/query')).at(-1) as PerformanceResourceTiming;
              if (!entry || entry.responseEnd === 0) { reject(new Error('missing complete HTTP resource timing')); return; }
              resolve({
                click_to_dom_commit_ms: domCommit - click,
                click_to_visible_commit_ms: visible - click,
                dispatch_to_visible_commit_ms: visible - state.latencyState.dispatch,
                click_to_dispatch_ms: state.latencyState.dispatch - click,
                http_ms: entry.responseEnd - entry.requestStart,
                fetch_resource_ms: entry.responseEnd - entry.startTime,
              });
            }));
          });
          button.addEventListener('click', () => {
            click = performance.now();
            observer.observe(document.querySelector('.results')!, { subtree: true, childList: true, attributes: true, characterData: true });
          }, { once: true, capture: true });
        });
      }, { name, expected });
      const responsePromise = page.waitForResponse(r => r.url().endsWith('/api/query'));
      await page.getByRole('button', { name, exact: true }).click();
      const response = await responsePromise;
      expect(response.status()).toBe(200);
      const body = await response.json() as QueryResponse;
      const timing = await page.evaluate(() => (window as unknown as { latencySample: Promise<Record<string, number>> }).latencySample);
      expect(body.result.aggregate.count).toBe(narrow ? 100_000 : 1_000_000);
      if (i >= 4) samples.push({
        index: i - 4, range_percent: narrow ? 10 : 100, engine: 'indexed',
        request: response.request().postDataJSON(), status: response.status(),
        matches: body.result.aggregate.count, ...timing, server: body.timing,
      });
    }
    expect(errors).toEqual([]);
  } finally {
    const output = process.env.CHRONOLENS_LATENCY_REPORT;
    if (!output) throw new Error('Missing report path');
    await writeFile(output, JSON.stringify({
      schema: 1, recorded_utc: new Date().toISOString(),
      boundary: 'Capture-phase preset click to matching result DOM (aria-busy=false, expected count visibly within the viewport, timeline SVG) followed by two requestAnimationFrame callbacks: a paint opportunity, not a physical display presentation timestamp. Dispatch is the fetch call; HTTP is ResourceTiming requestStart through responseEnd. App debounce is included only in click timings. Server query/profile/work exclude HTTP and rendering.',
      policy: 'Sequential real Chromium clicks, indexed engine, alternating 10%/100%, four unrecorded warmup clicks then 40 individual samples, no retries or latency thresholds; no concurrent load during browser phase.',
      environment: { node: process.version, platform: process.platform, arch: process.arch, cpu: os.cpus()[0]?.model, logical_cpus: os.cpus().length, browser: browser.version(), headless: true, viewport: page.viewportSize(), client_server: 'same shared host' },
      dataset: meta, errors, samples,
    }, null, 2) + '\n', { flag: 'wx' });
  }
});
