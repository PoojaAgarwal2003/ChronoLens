import { spawn } from 'node:child_process';
import { execFileSync } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';
import os from 'node:os';

const web = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const root = resolve(web, '..');
const prefix = process.env.CHRONOLENS_EXPERIMENT_PREFIX ?? `latency-${new Date().toISOString().replaceAll(/[:.]/g, '-')}`;
if (!/^[a-zA-Z0-9-]{1,100}$/.test(prefix)) throw new Error('Invalid report prefix');
const format = process.env.CHRONOLENS_EXPERIMENT_FORMAT ?? 'snapshot';
if (!['jsonl', 'snapshot'].includes(format)) throw new Error('Unsupported input format');
const output = join(root, 'benchmarks', 'results');
const cache = join(web, 'node_modules', '.cache');
await mkdir(cache, { recursive: true });
const work = await mkdtemp(join(cache, 'chronolens-latency-'));
const go = process.env.CHRONOLENS_GO ?? 'go';
const extension = process.platform === 'win32' ? '.exe' : '';
const serverExe = join(work, `server${extension}`);
const loadExe = join(work, `load${extension}`);
const dataset = join(work, 'events.jsonl');
const snapshot = join(work, 'events.clens');
let server;
let active;
let serverExited;
const commands = [];
const sourceFiles = ['internal/query/profile.go', 'internal/query/profile_benchmark_test.go', 'web/src/main.tsx', 'web/experiments/run.mjs', 'web/experiments/latency.spec.ts', 'web/package-lock.json'];
const provenance = {
  commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
  status: execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim(),
  go: execFileSync(go, ['version'], { encoding: 'utf8' }).trim(),
  sources: Object.fromEntries(await Promise.all(sourceFiles.map(async path => [path, await hash(join(root, path))]))),
};

async function run(command, args, env = {}) {
  commands.push({ command, args });
  const child = spawn(command, args, { cwd: root, stdio: 'inherit', windowsHide: true, env: { ...process.env, ...env } });
  active = child;
  try {
    const [code, signal] = await once(child, 'exit');
    if (code !== 0) throw new Error(`${command} exited ${code} / ${signal}`);
  } finally { active = undefined; }
}
async function hash(path) {
  const result = createHash('sha256');
  for await (const chunk of createReadStream(path)) result.update(chunk);
  return result.digest('hex');
}
function interrupt() {
  active?.kill('SIGTERM');
  server?.kill('SIGTERM');
}
process.on('SIGINT', interrupt);
process.on('SIGTERM', interrupt);
try {
  await run(go, ['build', '-o', serverExe, './cmd/server']);
  await run(go, ['build', '-o', loadExe, './cmd/load']);
  await run(go, ['run', './cmd/generator', '-events', '1000000', '-services', '16', '-seed', '42', '-output', dataset]);
  if (format === 'snapshot') await run(go, ['run', './cmd/pack', '-input', dataset, '-output', snapshot, '-max-events', '1000000']);
  server = spawn(serverExe, ['-input', format === 'snapshot' ? snapshot : dataset, '-format', format, '-max-events', '1000000', '-web', join(web, 'dist'), '-listen', '127.0.0.1:0'], { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'inherit'] });
  serverExited = once(server, 'exit');
  const baseURL = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('server startup timed out')), 120_000);
    let text = '';
    server.stdout.on('data', chunk => {
      text += chunk;
      const match = text.match(/ChronoLens listening on (http:\/\/127\.0\.0\.1:\d+)/);
      if (match) { clearTimeout(timeout); resolve(match[1]); }
    });
    server.once('error', error => { clearTimeout(timeout); reject(error); });
    server.once('exit', code => { clearTimeout(timeout); reject(new Error(`server exited early: ${code}`)); });
  });
  const metadata = await (await fetch(`${baseURL}/api/meta`, { signal: AbortSignal.timeout(5000) })).json();
  if (metadata.rows !== 1_000_000 || metadata.input_format !== format) throw new Error('Wrong dataset');
  const payload = JSON.stringify({ engine: 'indexed', from_us: metadata.min_timestamp_us, to_us: null, service: '', status: 0, buckets: 100 });
  for (const [name, endpoint, rate, inflight, timeout] of [
    ['query-baseline', 'query', 2, 2, '1s'],
    ['compare-baseline', 'compare', 2, 8, '1s'],
    ['compare-overload', 'compare', 40, 8, '1s'],
    ['compare-local-cap', 'compare', 40, 1, '1s'],
    ['compare-cancel', 'compare', 10, 2, '20ms'],
    ['compare-recovery', 'compare', 2, 2, '1s'],
  ]) {
    await run(loadExe, ['-target', `${baseURL}/api/${endpoint}`, '-payload', payload, '-duration', '5s', '-rate', String(rate), '-inflight', String(inflight), '-timeout', timeout, '-output', join(output, `${prefix}-${name}.json`)]);
  }
  await run(process.execPath, [join(web, 'node_modules', '@playwright', 'test', 'cli.js'), 'test', '--config', join(web, 'playwright.latency.config.ts')], {
    CHRONOLENS_LATENCY_URL: baseURL,
    CHRONOLENS_LATENCY_REPORT: join(output, `${prefix}-browser.json`),
  });
  await writeFile(join(output, `${prefix}-environment.json`), JSON.stringify({
    recorded_utc: new Date().toISOString(), dataset: metadata, provenance,
    generator: { events: 1_000_000, services: 16, seed: 42, profile: 'uniform', interval: '1ms', start: '2026-01-01T00:00:00Z' },
    jsonl_sha256: await hash(dataset), snapshot_sha256: format === 'snapshot' ? await hash(snapshot) : null,
    node: process.version, platform: process.platform, arch: process.arch, cpu: os.cpus()[0]?.model,
    logical_cpus: os.cpus().length, total_memory_bytes: os.totalmem(), free_memory_bytes_at_end: os.freemem(),
    caveats: 'Client, browser and server on same shared host; warm filesystem/cache; sequential phases, no isolation or cold-disk claims. Cancellation phase uses client deadlines, not proof of exact server admission; deterministic Go tests cover accepted cancellation and shared-slot recovery.',
    commands,
  }, null, 2) + '\n', { flag: 'wx' });
} finally {
  if (active?.exitCode === null) { active.kill('SIGTERM'); await once(active, 'exit'); }
  if (server?.exitCode === null) { server.kill('SIGTERM'); await serverExited; }
  await rm(work, { recursive: true, force: true, maxRetries: 8, retryDelay: 150 });
  process.off('SIGINT', interrupt);
  process.off('SIGTERM', interrupt);
}
