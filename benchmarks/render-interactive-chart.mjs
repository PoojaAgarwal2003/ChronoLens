import { readFileSync, writeFileSync } from 'node:fs';
import assert from 'node:assert/strict';

const [beforePath, afterPath, output] = process.argv.slice(2);
if (!beforePath || !afterPath || !output) throw new Error('Usage: node render-interactive-chart.mjs BEFORE-browser.json AFTER-browser.json OUTPUT.svg');
const before = JSON.parse(readFileSync(beforePath, 'utf8'));
const after = JSON.parse(readFileSync(afterPath, 'utf8'));
assert.deepEqual(before.environment, after.environment, 'Browser environments differ');
for (const key of ['rows', 'input_format', 'min_timestamp_us', 'max_timestamp_us', 'services', 'statuses']) {
  assert.deepEqual(before.dataset[key], after.dataset[key], `Dataset ${key} differs`);
}
assert.equal(before.dataset.rows, 1000000);
assert.equal(before.dataset.input_format, 'snapshot');
const metrics = ['click_to_visible_commit_ms', 'click_to_dispatch_ms'];
for (const report of [before, after]) {
  assert.deepEqual(report.errors, []);
  assert.equal(report.samples.length, 40, 'Expected 40 individual samples');
  for (const [i, sample] of report.samples.entries()) {
    assert.equal(sample.index, i);
    assert.equal(sample.range_percent, i % 2 === 0 ? 10 : 100);
    assert.equal(sample.matches, sample.range_percent === 10 ? 100000 : 1000000);
    assert.equal(sample.status, 200);
    assert.match(sample.exact_result_sha256, /^[0-9a-f]{64}$/);
    for (const value of [...metrics.map(key => sample[key]), sample.server.query_ms, sample.server.profile_ms]) {
      assert.ok(typeof value === 'number' && Number.isFinite(value) && value >= 0, 'Missing or invalid timing');
    }
  }
}
for (let i = 0; i < 40; i++) {
  assert.deepEqual(before.samples[i].request, after.samples[i].request, 'Request filters differ');
  assert.equal(before.samples[i].exact_result_sha256, after.samples[i].exact_result_sha256, 'Exact aggregate/profile outputs differ');
}
function stats(report, range, field) {
  const values = report.samples.filter(s => s.range_percent === range).map(s => field(s)).sort((a, b) => a - b);
  return { n: values.length, mean: values.reduce((a, b) => a + b, 0) / values.length, p50: values[Math.ceil(values.length * .5) - 1], p95: values[Math.ceil(values.length * .95) - 1] };
}
const summary = [10, 100].map(range => ({
  range, before: {
    click: stats(before, range, s => s.click_to_visible_commit_ms),
    dispatch: stats(before, range, s => s.click_to_dispatch_ms),
    query: stats(before, range, s => s.server.query_ms),
    profile: stats(before, range, s => s.server.profile_ms),
  }, after: {
    click: stats(after, range, s => s.click_to_visible_commit_ms),
    dispatch: stats(after, range, s => s.click_to_dispatch_ms),
    query: stats(after, range, s => s.server.query_ms),
    profile: stats(after, range, s => s.server.profile_ms),
  },
}));
const rows = summary.flatMap(s => [
  { label: `${s.range}% window · click median`, before: s.before.click.p50, after: s.after.click.p50 },
  { label: `${s.range}% window · server profile mean`, before: s.before.profile.mean, after: s.after.profile.mean },
]);
const max = Math.ceil(Math.max(...rows.flatMap(r => [r.before, r.after])) / 50) * 50;
const bars = rows.map((row, i) => {
  const y = 145 + i * 100;
  return `<text x="30" y="${y - 12}" class="label">${row.label}</text>
<rect x="355" y="${y - 22}" width="${(row.before / max * 490).toFixed(2)}" height="23" fill="#a65b00"/>
<text x="${(365 + row.before / max * 490).toFixed(2)}" y="${y - 5}">${row.before.toFixed(2)} ms</text>
<rect x="355" y="${y + 8}" width="${(row.after / max * 490).toFixed(2)}" height="23" fill="#007b74"/>
<text x="${(365 + row.after / max * 490).toFixed(2)}" y="${y + 25}">${row.after.toFixed(2)} ms</text>`;
}).join('\n');
const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="980" height="615" viewBox="0 0 980 615" role="img" aria-labelledby="title desc">
<title id="title">ChronoLens: measured interactive profile and preset improvement</title>
<desc id="desc">Same-host one-million-event snapshot. Twenty individual samples per range and version. Brown is baseline, teal is optimized. Click bars are nearest-rank medians; server chart bars are arithmetic means, not query-only timings.</desc>
<style>text{font:15px system-ui,sans-serif;fill:#172d38}.label{font-weight:600}.note{font-size:13px}</style>
<rect width="980" height="615" fill="#f8fafb"/>
<text x="30" y="38" style="font-size:24px;font-weight:700">Exact charts. Less waiting.</text>
<text x="30" y="66">1,000,000 events · 16 services · real Chromium · same shared Windows host</text>
<rect x="30" y="85" width="15" height="15" fill="#a65b00"/><text x="52" y="98">Baseline</text>
<rect x="155" y="85" width="15" height="15" fill="#007b74"/><text x="177" y="98">Optimized profile + immediate presets</text>
${bars}
<line x1="355" x2="845" y1="492" y2="492" stroke="#71808a"/>
<text x="355" y="513">0 ms</text><text x="810" y="513">${max} ms</text>
<text x="30" y="547" class="note">Click → matching visible DOM + two animation frames (paint opportunity, not display presentation).</text>
<text x="30" y="570" class="note">N = 20 per bar. Server profile excludes aggregate query, HTTP, and rendering. No portable SLO claim.</text>
<text x="30" y="593" class="note">Reproduce: benchmarks/interactive-performance.md · raw samples retained without retries or trimming.</text>
</svg>
`;
writeFileSync(output, svg, { flag: 'wx' });
console.log(JSON.stringify(summary, null, 2));
