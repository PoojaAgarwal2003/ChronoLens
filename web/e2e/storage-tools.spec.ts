import { expect, test } from '@playwright/test';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const engines = ['row', 'columnar', 'indexed'];

function invoke(script: string, ...args: string[]) {
  const result = spawnSync(process.execPath, [resolve(root, 'benchmarks', script), ...args], { encoding: 'utf8' });
  expect(result.error).toBeUndefined();
  return result;
}

function failed(result: ReturnType<typeof invoke>, message?: string) {
  expect(result.status).toBe(1);
  expect(result.stdout).toBe('');
  expect(result.stderr).not.toBe('');
  if (message) expect(result.stderr).toContain(message);
}

function sha256(bytes: Buffer | string) {
  return createHash('sha256').update(bytes).digest('hex');
}

// Deliberately bit-at-a-time, unlike the verifier's lookup-table implementation.
function crc32c(bytes: Buffer) {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0x82f63b78 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function repairIntegrity(bytes: Buffer) {
  let offset = 16;
  while (bytes.toString('ascii', offset, offset + 4) === 'BLK1') {
    const end = offset + 16 + bytes.readUInt32LE(offset + 12);
    bytes.writeUInt32LE(crc32c(bytes.subarray(offset, end)), end);
    offset = end + 4;
  }
  createHash('sha256').update(bytes.subarray(0, offset + 20)).digest().copy(bytes, offset + 20);
}

test('streaming verifier matches every event across actual Go-packed blocks and rejects malformed inputs', async ({}, info) => {
  test.setTimeout(120_000);
  mkdirSync(info.outputDir, { recursive: true });
  const sourcePath = info.outputPath('source.jsonl');
  const snapshotPath = info.outputPath('source.clens');
  const events = Array.from({ length: 5000 }, (_, i) => ({
    timestamp_us: i === 4999 ? Number.MAX_SAFE_INTEGER : Math.floor(i / 2),
    service: i % 2 ? 'service-002' : 'service-001',
    duration_us: i % 3 ? i : 0xffffffff,
    status: i % 4 ? 200 : 599,
  }));
  const source = events.map(event => JSON.stringify(event)).join('\r\n') + '\r\n';
  writeFileSync(sourcePath, source);
  const packed = spawnSync('go', ['run', './cmd/pack', '-input', sourcePath, '-output', snapshotPath, '-max-events', '5000'],
    { cwd: root, encoding: 'utf8', timeout: 100_000 });
  expect(packed.error).toBeUndefined();
  expect(packed.status, packed.stderr).toBe(0);
  const snapshot = readFileSync(snapshotPath);
  const result = invoke('compare-snapshot.mjs', sourcePath, snapshotPath, '5000');
  expect(result.status, result.stderr).toBe(0);
  expect(result.stderr).toBe('');
  expect(JSON.parse(result.stdout)).toMatchObject({
    events: 5000, blocks: 2, records_equal: true,
    source_sha256: sha256(source), snapshot_sha256: sha256(snapshot),
    source_bytes: Buffer.byteLength(source), snapshot_bytes: snapshot.length,
    crc32c_valid: true, snapshot_prefix_sha256_valid: true,
  });
  expect(crc32c(Buffer.from('123456789'))).toBe(0xe3069283);
  const changedSource = info.outputPath('changed.jsonl');
  for (const field of ['timestamp_us', 'service', 'duration_us', 'status'] as const) {
    const changed = events.map(event => ({ ...event }));
    if (field === 'service') changed[4100].service = 'other-service';
    else changed[4100][field]++;
    writeFileSync(changedSource, changed.map(event => JSON.stringify(event)).join('\n'));
    failed(invoke('compare-snapshot.mjs', changedSource, snapshotPath, '5000'), field);
  }
  // This preserves aggregates and timestamps: only full record parity catches it.
  const swapped = events.map(event => ({ ...event }));
  [swapped[0].service, swapped[1].service] = [swapped[1].service, swapped[0].service];
  writeFileSync(changedSource, swapped.map(event => JSON.stringify(event)).join('\n'));
  failed(invoke('compare-snapshot.mjs', changedSource, snapshotPath, '5000'), 'service differs');
  for (const count of ['0', '-1', '1.5', '1e3', '100000001', '9007199254740993', '4999', '5001']) {
    failed(invoke('compare-snapshot.mjs', sourcePath, snapshotPath, count));
  }
  writeFileSync(changedSource, source.trimEnd());
  const noNewline = invoke('compare-snapshot.mjs', changedSource, snapshotPath, '5000');
  expect(noNewline.status, noNewline.stderr).toBe(0);
  expect(JSON.parse(noNewline.stdout).source_sha256).toBe(sha256(source.trimEnd()));
  const invalidSource = [
    source.replace('"timestamp_us":0', '"timestamp_us":9007199254740993'),
    source.replace('"timestamp_us":0', '"timestamp_us":0.00000000000000001'),
    source.replace('"timestamp_us":0', '"timestamp_us":0.00000000000000000000000000000000000000000000000001'),
    source.replace('"timestamp_us":0', '"timestamp_us":0,"timestamp_us":0'),
    source.replace('"service":"service-001"', '"service":"\\ud800"'),
    source.replace('"duration_us":4294967295', '"duration_us":null'),
    source.replace('"status":599', '"unknown":599'),
    source.split('\r\n').slice(0, -2).join('\n'),
    source + JSON.stringify(events.at(-1)) + '\n',
    ' '.repeat(4098) + '\n',
  ];
  for (const invalid of invalidSource) {
    writeFileSync(changedSource, invalid);
    failed(invoke('compare-snapshot.mjs', changedSource, snapshotPath, '5000'));
  }
  const changedSnapshot = info.outputPath('changed.clens');
  const firstRows = snapshot.readUInt32LE(20);
  const firstLength = snapshot.readUInt32LE(28);
  const columns = 32 + firstLength - firstRows * 16;
  const mutations: Array<[string, (bytes: Buffer) => void, boolean]> = [
    ['CRC32C', bytes => { bytes[columns + firstRows * 10] ^= 1; }, false],
    ['SHA-256', bytes => { bytes[bytes.length - 1] ^= 1; }, false],
    ['magic', bytes => { bytes[0] = 0; }, false],
    ['magic', bytes => { bytes[0] |= 0x80; }, false],
    ['version', bytes => bytes.writeUInt16LE(2, 8), false],
    ['flags', bytes => bytes.writeUInt16LE(1, 10), false],
    ['reserved', bytes => bytes.writeUInt32LE(1, 12), false],
    ['row count', bytes => bytes.writeUInt32LE(0, 20), false],
    ['row count', bytes => bytes.writeUInt32LE(4097, 20), false],
    ['dictionary count', bytes => bytes.writeUInt32LE(0, 24), false],
    ['dictionary count', bytes => bytes.writeUInt32LE(4097, 24), false],
    ['payload length', bytes => bytes.writeUInt32LE(0xffffffff, 28), false],
    ['payload length', bytes => bytes.writeUInt32LE(1, 28), false],
    ['name length', bytes => bytes.writeUInt16LE(0, 32), true],
    ['name length', bytes => bytes.writeUInt16LE(129, 32), true],
    ['encoded data', bytes => { bytes[34] = 0xff; }, true],
    ['duplicate', bytes => bytes.copy(bytes, 47, 34, 45), true],
    ['service ID', bytes => bytes.writeUInt16LE(2, columns + firstRows * 8), true],
    ['timestamps', bytes => bytes.writeBigInt64LE(-1n, columns), true],
    ['timestamps', bytes => bytes.writeBigInt64LE(0n, columns + 4 * 8), true],
    ['safe integer', bytes => bytes.writeBigInt64LE(9007199254740992n, columns), true],
    ['status', bytes => bytes.writeUInt16LE(99, columns + firstRows * 14), true],
    ['totals', bytes => bytes.writeBigUInt64LE(4999n, bytes.length - 48), true],
    ['totals', bytes => bytes.writeBigUInt64LE(3n, bytes.length - 40), true],
  ];
  for (const [message, mutate, repair] of mutations) {
    const bytes = Buffer.from(snapshot);
    mutate(bytes);
    if (repair) repairIntegrity(bytes);
    writeFileSync(changedSnapshot, bytes);
    failed(invoke('compare-snapshot.mjs', sourcePath, changedSnapshot, '5000'), message);
  }
  for (const length of [0, 15, 18, 32, columns, snapshot.length - 1, snapshot.length - 52]) {
    writeFileSync(changedSnapshot, snapshot.subarray(0, length));
    failed(invoke('compare-snapshot.mjs', sourcePath, changedSnapshot, '5000'));
  }
  writeFileSync(changedSnapshot, Buffer.concat([snapshot, Buffer.from([0])]));
  failed(invoke('compare-snapshot.mjs', sourcePath, changedSnapshot, '5000'), 'trailing bytes');
  failed(invoke('compare-snapshot.mjs', sourcePath, info.outputPath('missing.clens'), '5000'));
  failed(invoke('compare-snapshot.mjs', info.outputPath('missing.jsonl'), snapshotPath, '5000'));
});

function report(inputFormat: 'jsonl' | 'snapshot') {
  const source = {
    input_format: inputFormat, events: 8, services: 2, bytes: inputFormat === 'jsonl' ? 1024 : 256,
    sha256: (inputFormat === 'jsonl' ? 'a' : 'b').repeat(64), min_us: 0, max_us: 7, time_span_us: 8,
  };
  const cases = [
    { name: 'all', filter: { from_us: 0, to_us: 8, service: '', status: 0 } },
    { name: 'service', filter: { from_us: 0, to_us: null, service: 'api<&"', status: 0 } },
  ];
  return {
    schema_version: 1, source, cases,
    engines: engines.map((engine, index) => ({
      engine,
      load: { ms: (inputFormat === 'jsonl' ? 1000 : 500) * (index + 1), bytes: source.bytes, sha256: source.sha256 },
      heap: { delta_heap_alloc_bytes: 512 },
      cases: cases.map((item, index) => ({
        name: item.name,
        result: {
          aggregate: {
            count: index ? 4 : 8, error_count: index ? 1 : 2, duration_sum_us: index ? 40 : 80,
            mean_duration_us: 10, min_duration_us: 10, max_duration_us: 10,
          },
        },
        batch_mean_ms: { min: 0.01, median: 0.02, max: 0.03, stable_window: true },
      })),
    })),
  };
}

test('storage chart plots six measured load bars, source bytes, and honest scope; never overwrites', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const jsonl = info.outputPath('jsonl-report.json'), snapshot = info.outputPath('snapshot-report.json');
  const output = info.outputPath('storage.svg');
  writeFileSync(jsonl, JSON.stringify(report('jsonl')));
  writeFileSync(snapshot, JSON.stringify(report('snapshot')));
  const result = invoke('render-storage-chart.mjs', jsonl, snapshot, output);
  expect(result.status, result.stderr).toBe(0);
  const svg = readFileSync(output, 'utf8');
  expect(svg).toContain('aria-labelledby="title desc"');
  expect(svg).toContain('<title id="title">');
  expect(svg).toContain('<desc id="desc">');
  expect(svg).toContain('STORAGE &amp; LOADING');
  expect(svg).toContain('1,024 bytes');
  expect(svg).toContain('256 bytes');
  expect(svg).toContain('linear; zero baseline');
  expect(svg).toContain('one observed load per layout; not p95/cold-disk');
  expect(svg).toContain('Conversion cost not included');
  expect(svg).toContain('CRC32C');
  expect(svg).toContain('embedded prefix SHA-256');
  expect(svg).toContain('whole-file source SHA-256');
  expect(svg).not.toMatch(/<script|<image|api<&/);
  expect(svg.match(/class="load-bar"/g)).toHaveLength(6);
  for (const engine of engines) expect(svg.match(new RegExp(`data-engine="${engine}"`, 'g'))).toHaveLength(2);
  for (const ms of [500, 1000, 1500, 2000, 3000]) expect(svg).toContain(`data-load-ms="${ms}"`);
  for (const value of ['0.5', '1', '1.5', '2', '3']) expect(svg).toContain(`>${value} s</text>`);
  const bars = [...svg.matchAll(/class="load-bar"[^>]*data-load-ms="([^"]+)"[^>]*width="([^"]+)"/g)];
  for (const bar of bars) expect(Number(bar[2])).toBeCloseTo(Number(bar[1]) / 3000 * 730, 8);
  failed(invoke('render-storage-chart.mjs', jsonl, snapshot, output), 'EEXIST');
  expect(readFileSync(output, 'utf8')).toBe(svg);
});

test('storage chart rejects incomparable sources, filters, aggregates, unresolved loads and invalid provenance', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const jsonl = info.outputPath('jsonl-report.json'), snapshot = info.outputPath('snapshot-report.json');
  const output = info.outputPath('rejected.svg');
  writeFileSync(jsonl, JSON.stringify(report('jsonl')));
  type Report = ReturnType<typeof report>;
  const mutations: Array<(value: Report) => void> = [
    value => { Reflect.deleteProperty(value.source, 'input_format'); },
    value => { value.source.input_format = 'jsonl'; },
    value => { value.source.events = 9; },
    value => { value.source.services = 1; },
    value => { value.source.min_us = 1; value.source.max_us = 8; },
    value => { value.cases[0].filter.to_us = 7; },
    value => { value.engines[1].cases[1].result.aggregate.error_count = 0; },
    value => { Reflect.set(value.engines[0].load, 'ms', null); },
    value => { Reflect.deleteProperty(value.engines[0].load, 'ms'); },
    value => { Reflect.set(value.engines[0].load, 'ms', '1000'); },
    value => { value.engines[0].load.ms = 0; },
    value => { value.engines[0].load.ms = -1; },
    value => { value.engines[0].load.ms = Infinity; },
    value => { Reflect.set(value.engines[0].load, 'stable_window', false); },
    value => { value.engines[0].load.bytes++; },
    value => { value.engines[0].load.sha256 = 'c'.repeat(64); },
    value => { value.engines[0].engine = 'unknown'; },
    value => { value.engines[0].engine = 'indexed'; },
    value => { value.engines[0].cases.pop(); },
    value => { Reflect.set(value.engines[0].cases[0].result, 'aggregate', null); },
    value => { value.source.bytes = Number.MAX_SAFE_INTEGER + 1; },
  ];
  for (const mutate of mutations) {
    const value = report('snapshot');
    mutate(value);
    writeFileSync(snapshot, JSON.stringify(value));
    failed(invoke('render-storage-chart.mjs', jsonl, snapshot, output));
    expect(existsSync(output)).toBe(false);
  }
  writeFileSync(snapshot, JSON.stringify(report('snapshot')));
  failed(invoke('render-storage-chart.mjs', snapshot, jsonl, output), 'input_format');
});

test('storage chart compares uint64 aggregate sums exactly rather than rounding to Number', async ({}, info) => {
  mkdirSync(info.outputDir, { recursive: true });
  const jsonl = info.outputPath('large-sum-jsonl.json'), snapshot = info.outputPath('large-sum-snapshot.json');
  const makeLargeSumReport = (inputFormat: 'jsonl' | 'snapshot') => {
    const value = report(inputFormat);
    value.source.events = 100_000_000;
    for (const engine of value.engines) {
      for (const item of engine.cases) {
        item.result.aggregate = {
          count: 100_000_000, error_count: 0, duration_sum_us: 100_000_000_000_000_000,
          mean_duration_us: 1_000_000_000, min_duration_us: 999_999_999, max_duration_us: 1_000_000_001,
        };
      }
    }
    return JSON.stringify(value);
  };
  writeFileSync(jsonl, makeLargeSumReport('jsonl'));
  const snapshotText = makeLargeSumReport('snapshot');
  writeFileSync(snapshot, snapshotText);
  const output = info.outputPath('large-sum.svg');
  const valid = invoke('render-storage-chart.mjs', jsonl, snapshot, output);
  expect(valid.status, valid.stderr).toBe(0);
  writeFileSync(snapshot, snapshotText.replace('"duration_sum_us":100000000000000000', '"duration_sum_us":100000000000000001'));
  const rejected = info.outputPath('unequal-sum.svg');
  failed(invoke('render-storage-chart.mjs', jsonl, snapshot, rejected), 'aggregate differs');
  expect(existsSync(rejected)).toBe(false);
});
