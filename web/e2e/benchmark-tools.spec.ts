import { expect, test } from '@playwright/test';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const evidence = resolve(root, 'benchmarks/results/windows-10m-20260914.json');

function invoke(script: string, ...args: string[]) {
  const result = spawnSync(process.execPath, [resolve(root, 'benchmarks', script), ...args], { encoding: 'utf8' });
  expect(result.error).toBeUndefined();
  return result;
}

test('independent oracle matches hand-calculated aggregates and ceiling range boundaries', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const input = info.outputPath('uniform.jsonl');
  const events = Array.from({ length: 1001 }, (_, i) => ({
    timestamp_us: i, service: i % 2 ? 'service-002' : 'service-001',
    duration_us: 100 + i, status: i % 4 ? 200 : 500,
  }));
  writeFileSync(input, events.map(event => JSON.stringify(event)).join('\n') + '\n');
  const result = invoke('uniform-oracle.mjs', input, '1001');
  expect(result.status).toBe(0);
  const report = JSON.parse(result.stdout);
  expect(report.cases[0]).toMatchObject({
    filter: { from_us: 499, to_us: 501 }, count: 2, error_count: 1,
    duration_sum_us: '1199', mean_duration_us: 599.5,
  });
  expect(report.cases[3]).toMatchObject({
    count: 1001, error_count: 251, duration_sum_us: '600600',
    mean_duration_us: 600, min_duration_us: 100, max_duration_us: 1100,
  });
  expect(report.cases[4]).toMatchObject({ count: 501, duration_sum_us: '300600' });
  for (const count of ['0', '1000', '1002']) {
    const invalid = invoke('uniform-oracle.mjs', input, count);
    expect(invalid.status).toBe(1);
    expect(invalid.stdout).toBe('');
    expect(invalid.stderr).not.toBe('');
  }
});

test('published measurement chart is reproducible and cannot overwrite an existing output', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const output = info.outputPath('chart.svg');
  expect(invoke('render-chart.mjs', evidence, output).status).toBe(0);
  const svg = readFileSync(output, 'utf8');
  expect(svg).toBe(readFileSync(resolve(root, 'docs/images/ten-million-benchmark.svg'), 'utf8'));
  expect(svg.match(/<polyline /g)).toHaveLength(3);
  const duplicate = invoke('render-chart.mjs', evidence, output);
  expect(duplicate.status).toBe(1);
  expect(duplicate.stderr).toContain('EEXIST');
  expect(readFileSync(output, 'utf8')).toBe(svg);
});

test('chart rejects unstable query summaries and never converts null loading time into zero', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const report = JSON.parse(readFileSync(evidence, 'utf8'));
  const input = info.outputPath('report.json');
  const output = info.outputPath('chart.svg');
  report.engines[0].cases[0].batch_mean_ms.stable_window = false;
  writeFileSync(input, JSON.stringify(report));
  expect(invoke('render-chart.mjs', input, output).status).toBe(1);
  expect(existsSync(output)).toBe(false);
  report.engines[0].cases[0].batch_mean_ms.stable_window = true;
  report.engines[0].load.ms = null;
  writeFileSync(input, JSON.stringify(report));
  expect(invoke('render-chart.mjs', input, output).status).toBe(0);
  expect(readFileSync(output, 'utf8')).toContain('Loading timings unresolved');
});

test('warm-query chart labels snapshot validation rather than JSONL parsing', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const report = JSON.parse(readFileSync(evidence, 'utf8'));
  report.source.input_format = 'snapshot';
  const input = info.outputPath('snapshot-report.json');
  const output = info.outputPath('snapshot-chart.svg');
  writeFileSync(input, JSON.stringify(report));
  expect(invoke('render-chart.mjs', input, output).status).toBe(0);
  const svg = readFileSync(output, 'utf8');
  expect(svg).toContain('snapshot integrity checks and source SHA-256');
  expect(svg).not.toContain('strict JSONL parsing');
});

test('interactive chart reproduces measured samples and rejects incomparable evidence', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const before = resolve(root, 'benchmarks/results/20260916-evening-baseline-browser.json');
  const after = resolve(root, 'benchmarks/results/20260916-evening-after-browser.json');
  const output = info.outputPath('interactive.svg');
  expect(invoke('render-interactive-chart.mjs', before, after, output).status).toBe(0);
  const svg = readFileSync(output, 'utf8');
  expect(svg).toBe(readFileSync(resolve(root, 'docs/images/interactive-performance.svg'), 'utf8'));
  expect(invoke('render-interactive-chart.mjs', before, after, output).stderr).toContain('EEXIST');
  expect(readFileSync(output, 'utf8')).toBe(svg);
  const original = JSON.parse(readFileSync(after, 'utf8'));
  const input = info.outputPath('invalid.json');
  const invalidOutput = info.outputPath('invalid.svg');
  for (const mutate of [
    (r: typeof original) => { r.samples[0].exact_result_sha256 = '0'.repeat(64); },
    (r: typeof original) => { r.samples[0].request.service = 'different'; },
    (r: typeof original) => { r.samples[0].server.profile_ms = null; },
    (r: typeof original) => { r.samples.pop(); },
    (r: typeof original) => { r.environment.browser = 'different'; },
    (r: typeof original) => { r.dataset.rows = 1000; },
  ]) {
    const invalid = structuredClone(original);
    mutate(invalid);
    writeFileSync(input, JSON.stringify(invalid));
    expect(invoke('render-interactive-chart.mjs', before, input, invalidOutput).status).toBe(1);
    expect(existsSync(invalidOutput)).toBe(false);
  }
});
