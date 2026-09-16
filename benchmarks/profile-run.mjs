import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { resolve, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import os from 'node:os';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const prefix = process.argv[2];
if (!prefix || !/^[a-zA-Z0-9-]{1,100}$/.test(prefix)) throw new Error('Supply a new report prefix');
const go = process.env.CHRONOLENS_GO ?? 'go';
const args = ['test', './internal/query', '-run', '^$', '-bench', '^BenchmarkProfile$', '-benchmem', '-benchtime=300ms', '-count=5'];
const sources = ['internal/query/profile.go', 'internal/query/profile_benchmark_test.go', 'internal/query/query_test.go', 'benchmarks/profile-run.mjs'];
const metadata = {
  started_utc: new Date().toISOString(),
  commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
  status: execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim(),
  go: execFileSync(go, ['version'], { encoding: 'utf8' }).trim(),
  node: process.version, platform: process.platform, arch: process.arch,
  cpu: os.cpus()[0]?.model, logical_cpus: os.cpus().length,
  command: [go, ...args],
  environment: Object.fromEntries(['GOMAXPROCS', 'GOGC', 'GOFLAGS'].map(key => [key, process.env[key] ?? null])),
  sources: Object.fromEntries(sources.map(path => [path, createHash('sha256').update(readFileSync(join(root, path))).digest('hex')])),
  policy: 'Five Go benchmark batch means per case, 300ms minimum each; not request percentiles. One million rows; fixtures and reference query outside b.Loop. 16 services: generatedInput seed 42, 1us interval, epoch start, 5% errors. 65536 services: round-robin IDs, timestamps i us, duration uint32(i*7919)%750000, every twentieth status 500. Same shared Windows host, no isolation.',
};
const output = join(root, 'benchmarks', 'results', prefix);
// Reserve both filenames before running so previous evidence is never overwritten.
const raw = output + '-go.txt';
writeFileSync(raw, '', { flag: 'wx' });
writeFileSync(output + '-go-environment.json', JSON.stringify(metadata, null, 2) + '\n', { flag: 'wx' });
const result = execFileSync(go, args, { cwd: root, encoding: 'utf8', maxBuffer: 1024 * 1024, timeout: 300_000 });
writeFileSync(raw, result);
console.log(result);
