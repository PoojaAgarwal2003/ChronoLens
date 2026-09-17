import { chromium, expect } from '@playwright/test';
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, mkdtemp, readFile, rename, rm, stat, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureName, validateVideo } from './validation.mjs';

const web = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const root = resolve(web, '..');
const name = captureName(process.argv[2] ?? `showcase-${new Date().toISOString().replaceAll(/[:.]/g, '-')}`);
if (process.argv.length > 3) throw new Error('Usage: npm run demo:capture -- [new-capture-name]');
const output = join(root, 'docs', 'media', name);
await mkdir(dirname(output), { recursive: true });
await mkdir(output); // Never overwrite a retained capture, even after a failed run.
const cache = join(web, 'node_modules', '.cache');
await mkdir(cache, { recursive: true });
const work = await mkdtemp(join(cache, 'chronolens-showcase-'));
const go = process.env.CHRONOLENS_GO || 'go';
const runGo = args => execFileSync(go, args, { cwd: root, stdio: 'inherit', windowsHide: true });
const hash = async path => {
  const digest = createHash('sha256');
  for await (const chunk of createReadStream(path)) digest.update(chunk);
  return digest.digest('hex');
};
let server;
let browser;
let videoServer;
const interrupted = () => { server?.kill('SIGTERM'); void browser?.close(); };
process.on('SIGINT', interrupted);
process.on('SIGTERM', interrupted);
try {
  const executable = join(work, process.platform === 'win32' ? 'server.exe' : 'server');
  const dataset = join(work, 'incident.jsonl');
  const generator = ['run', './cmd/generator', '-profile', 'incident', '-events', '100000',
    '-services', '16', '-seed', '42', '-output', dataset];
  runGo(['build', '-o', executable, './cmd/server']);
  runGo(generator);
  server = spawn(executable, ['-input', dataset, '-max-events', '100000', '-web', join(web, 'dist'),
    '-listen', '127.0.0.1:0'], { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'inherit'] });
  const baseURL = await new Promise((resolveURL, reject) => {
    const timeout = setTimeout(() => reject(new Error('Server startup timed out')), 120_000);
    let text = '';
    server.stdout.on('data', chunk => {
      text += chunk;
      const match = text.match(/ChronoLens listening on (http:\/\/127\.0\.0\.1:\d+)/);
      if (match) { clearTimeout(timeout); resolveURL(match[1]); }
    });
    server.once('error', error => { clearTimeout(timeout); reject(error); });
    server.once('exit', code => { clearTimeout(timeout); reject(new Error(`Server exited: ${code}`)); });
  });
  const metadata = await (await fetch(`${baseURL}/api/meta`, { signal: AbortSignal.timeout(5000) })).json();
  expect(metadata.rows).toBe(100000);
  expect(metadata.input_format).toBe('jsonl');
  browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 },
    recordVideo: { dir: output, size: { width: 1280, height: 900 } } });
  const page = await context.newPage();
  const video = page.video();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const started = new Date();
  const scenes = [];
  const pause = async (label, seconds = 6) => {
    scenes.push({ label, elapsed_seconds: (Date.now() - started.getTime()) / 1000 });
    await page.evaluate(() => { if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); });
    await page.mouse.move(1260, 15);
    await page.waitForTimeout(seconds * 1000);
  };
  const queryAction = async action => {
    const response = page.waitForResponse(response => response.url().endsWith('/api/query') && response.status() === 200);
    await action();
    const data = await (await response).json();
    await expect(page.getByTestId('match-count')).toHaveText(data.result.aggregate.count.toLocaleString('en-US'));
    return data;
  };
  await page.goto(baseURL);
  await expect(page.getByTestId('match-count')).toHaveText('100,000');
  await pause('Full dataset: generated incident and recovery');
  await page.screenshot({ path: join(output, 'desktop.png'), fullPage: true });
  await page.getByRole('heading', { name: 'Event timeline', exact: true }).scrollIntoViewIfNeeded();
  await pause('Timeline spike, error concentration, duration distribution');
  await page.evaluate(() => scrollTo(0, 0));
  await queryAction(() => page.getByLabel('Range start', { exact: false }).fill('470'));
  const window = await queryAction(() => page.getByLabel('Range end', { exact: false }).fill('530'));
  expect(window.result.aggregate.count).toBeGreaterThan(20000);
  await pause('47–53% time window surrounds the five-second incident');
  await queryAction(() => page.getByLabel('Service', { exact: true }).selectOption('service-001'));
  const filtered = await queryAction(() => page.getByLabel('HTTP status').selectOption('500'));
  expect(filtered.result.aggregate.count).toBeGreaterThan(10000);
  expect(filtered.result.aggregate.error_count).toBe(filtered.result.aggregate.count);
  await pause('Service-001 failures: inspect the synthetic business signal');
  await page.getByRole('heading', { name: 'Under the hood', exact: true }).scrollIntoViewIfNeeded();
  await pause('Separate query and exact-chart costs; index work is not free');
  await page.getByRole('button', { name: 'Compare engines' }).click();
  await expect(page.getByTestId('comparison-table').locator('tbody tr')).toHaveCount(3);
  await expect(page.getByText('Equivalent aggregates across all three engines', { exact: false })).toBeVisible();
  await page.getByTestId('comparison-table').scrollIntoViewIfNeeded();
  await pause('Equivalent aggregates; warm batch means are not browser latency', 8);
  await page.evaluate(() => scrollTo(0, 0));
  await page.waitForTimeout(250);
  await page.screenshot({ path: join(output, 'comparison.png'), fullPage: true });
  expect(errors).toEqual([]);
  await context.close();
  await rename(await video.path(), join(output, 'demo.webm'));

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
  const mobilePage = await mobile.newPage();
  mobilePage.on('pageerror', error => errors.push(error.message));
  await mobilePage.goto(baseURL);
  await expect(mobilePage.getByTestId('match-count')).toHaveText('100,000');
  await mobilePage.evaluate(() => document.fonts.ready);
  expect(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await mobilePage.screenshot({ path: join(output, 'mobile.png'), fullPage: true });
  await mobile.close();

  // Decode the finalized artifact, not a live page or an inferred wall-clock duration.
  const videoPath = join(output, 'demo.webm');
  videoServer = createServer((_request, response) => {
    response.writeHead(200, { 'Content-Type': 'video/webm' });
    createReadStream(videoPath).pipe(response);
  });
  videoServer.listen(0, '127.0.0.1');
  await once(videoServer, 'listening');
  const verify = await browser.newPage();
  await verify.setContent(`<video muted src="http://127.0.0.1:${videoServer.address().port}/demo.webm"></video>`);
  const decoded = await verify.evaluate(async () => {
    const video = document.querySelector('video');
    await new Promise((resolve, reject) => {
      if (video.readyState >= 1) return resolve();
      video.onloadedmetadata = resolve;
      video.onerror = () => reject(new Error('Video metadata failed'));
    });
    if (!Number.isFinite(video.duration)) {
      video.currentTime = 1e10;
      await new Promise(resolve => video.onseeked = resolve);
    }
    const duration = video.duration;
    video.currentTime = 0;
    await video.play();
    await new Promise(resolve => setTimeout(resolve, 500));
    video.pause();
    return { duration, width: video.videoWidth, height: video.videoHeight,
      decodedFrames: video.getVideoPlaybackQuality().totalVideoFrames };
  });
  const recording = { ...decoded, bytes: (await stat(videoPath)).size };
  validateVideo(recording);
  expect(errors).toEqual([]);
  const sources = execFileSync('git', ['ls-files', 'cmd', 'internal', 'web/src', 'web/package.json', 'web/package-lock.json'],
    { cwd: root, encoding: 'utf8' }).trim().split('\n');
  sources.push('web/showcase/run.mjs', 'web/showcase/validation.mjs');
  const artifacts = Object.fromEntries(await Promise.all(['demo.webm', 'desktop.png', 'mobile.png', 'comparison.png']
    .map(async file => [file, { bytes: (await stat(join(output, file))).size, sha256: await hash(join(output, file)) }])));
  await writeFile(join(output, 'capture.json'), JSON.stringify({
    recorded_utc: started.toISOString(), completed_utc: new Date().toISOString(),
    source_commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
    source_status: execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim(),
    source_sha256: Object.fromEntries(await Promise.all(sources.map(async file => [file, await hash(join(root, file))]))),
    generator: { profile: 'incident', events: 100000, services: 16, seed: 42, interval: '1ms', start: '2026-01-01T00:00:00Z', error_percent: 5 },
    dataset_sha256: await hash(dataset), metadata, selected_time_percent: [47, 53],
    filtered_aggregate: filtered.result.aggregate, scenes, recording, artifacts,
    tools: { node: process.version, go: execFileSync(go, ['version'], { encoding: 'utf8' }).trim(),
      playwright: JSON.parse(await readFile(join(web, 'node_modules', '@playwright', 'test', 'package.json'))).version,
      chromium: browser.version() },
    checks: { page_errors: errors, mobile_no_horizontal_overflow: true, equivalent_engine_aggregates: true, video_decoded: true },
    caveats: 'Paced real UI, synthetic incident; no API mocks, narration, or timing overlays. Displayed timings are incidental observations, not benchmarks. Project license unselected; copied-code provenance unresolved: see THIRD_PARTY_NOTICES.md.'
  }, null, 2) + '\n', { flag: 'wx' });
  console.log(`Validated ${recording.duration.toFixed(2)}s / ${recording.bytes} bytes: ${output}`);
} finally {
  await browser?.close();
  if (videoServer) { videoServer.closeAllConnections(); await new Promise(resolve => videoServer.close(resolve)); }
  if (server && server.exitCode === null && server.signalCode === null) {
    const exited = once(server, 'exit');
    server.kill('SIGTERM');
    await exited;
  }
  await rm(work, { recursive: true, force: true, maxRetries: 8, retryDelay: 150 });
  process.off('SIGINT', interrupted);
  process.off('SIGTERM', interrupted);
}
