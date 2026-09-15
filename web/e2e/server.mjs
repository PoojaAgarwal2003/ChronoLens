import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { workspace } from './workspace.mjs';

const web = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const root = resolve(web, '..');
const format = process.env.CHRONOLENS_E2E_FORMAT ?? 'jsonl';
if (!['jsonl', 'snapshot'].includes(format)) throw new Error('Unsupported test input format');
// Keep all generated test artifacts inside the frontend's ignored dependency tree.
const { work, cache, token } = workspace();
mkdirSync(cache, { recursive: true });
mkdirSync(work);
writeFileSync(join(work, 'owner'), token, { flag: 'wx' });
const executable = join(work, process.platform === 'win32' ? 'server.exe' : 'server');
const dataset = join(work, 'events.jsonl');
const packed = join(work, 'events.clens');
const go = process.env.CHRONOLENS_GO || 'go';
let server;
let stopping = false;

function clean() {
  rmSync(work, { recursive: true, force: true, maxRetries: 8, retryDelay: 150 });
}
function run(args) {
  const result = spawnSync(go, args, { cwd: root, stdio: 'inherit', windowsHide: true });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`go ${args[0]} failed (${result.status})`);
}
function stop() {
  if (stopping) return;
  stopping = true;
  if (server && server.exitCode === null) server.kill('SIGTERM');
  else { clean(); process.exit(0); }
}
process.on('SIGINT', stop);
process.on('SIGTERM', stop);
// Teardown asks this live wrapper to stop its own child, never a saved/recycled PID.
setInterval(() => { if (existsSync(join(work, 'shutdown'))) stop(); }, 50).unref();
try {
  run(['build', '-o', executable, './cmd/server']);
  run(['run', './cmd/generator', '-events', '10000', '-services', '8', '-seed', '42', '-output', dataset]);
  if (format === 'snapshot') run(['run', './cmd/pack', '-input', dataset, '-output', packed, '-max-events', '10000']);
  server = spawn(executable, ['-input', format === 'snapshot' ? packed : dataset, '-format', format, '-web', join(web, 'dist'), '-listen', '127.0.0.1:8090', '-max-events', '10000'], {
    cwd: root, stdio: 'inherit', windowsHide: true,
  });
  server.on('error', error => { console.error(error); clean(); process.exit(1); });
  server.on('exit', code => { clean(); process.exit(stopping ? 0 : code ?? 1); });
} catch (error) {
  console.error(error);
  clean();
  process.exit(1);
}
