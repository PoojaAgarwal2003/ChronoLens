import { readFileSync, writeFileSync } from 'node:fs';

const escape = text => String(text).replace(/[&<>"']/g, c => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;',
}[c]));

function render(report) {
  // Schema-v1 reports published before snapshot support are explicitly JSONL.
  const inputFormat = report.source.input_format ?? 'jsonl';
  if (!['jsonl', 'snapshot'].includes(inputFormat)) throw new Error('Unsupported report input format');
  const names = ['row', 'columnar', 'indexed'];
  const colors = ['#f5b777', '#91adff', '#65e0c9'];
  const labels = ['0.1%', '1%', '10%', '100%'];
  const expected = ['time_0.1pct', 'time_1pct', 'time_10pct', 'time_100pct', 'service_only'];
  const engines = names.map(name => report.engines.find(engine => engine.engine === name));
  if (report.schema_version !== 1 || engines.some(engine => !engine)) {
    throw new Error('Expected a schema-v1 report containing all three engines');
  }
  for (const engine of engines) {
    for (let i = 0; i < expected.length; i++) {
      const item = engine.cases[i];
      if (item?.name !== expected[i] || !item.batch_mean_ms.stable_window ||
          !['min', 'median', 'max'].every(key => Number.isFinite(item.batch_mean_ms[key]) && item.batch_mean_ms[key] > 0)) {
        throw new Error('Chart requires positive, stable batch summaries for the standard cases');
      }
    }
  }
  const values = engines.flatMap(e => e.cases.slice(0, 4).flatMap(c => [c.batch_mean_ms.min, c.batch_mean_ms.max]));
  const low = Math.floor(Math.log10(Math.min(...values)));
  const high = Math.max(low + 1, Math.ceil(Math.log10(Math.max(...values))));
  const x = (i, e) => 160 + i * 310 + (e - 1) * 9;
  const y = value => 600 - (Math.log10(value) - low) / (high - low) * 330;
  const indexed = engines[2].cases[0];
  const format = number => number.toLocaleString('en-US');
  const text = (px, py, content, size = 14, fill = '#a8bec4', extra = '') =>
    `<text x="${px}" y="${py}" font-size="${size}" fill="${fill}" ${extra}>${escape(content)}</text>`;
  const parts = [
    '<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="920" viewBox="0 0 1280 920" role="img" aria-labelledby="title desc">',
    '<title id="title">ChronoLens: measured time selectivity across three query engines</title>',
    '<desc id="desc">Warm batch means on a logarithmic millisecond axis. Error bars show min and max batch means, not request percentiles. Loading and browser rendering are excluded.</desc>',
    '<rect width="1280" height="920" rx="18" fill="#0a1418"/>',
    '<g font-family="Arial, Helvetica, sans-serif">',
    text(40, 45, 'CHRONOLENS / MEASURED, NOT PROMISED', 13, '#65e0c9', 'letter-spacing="2"'),
    text(40, 85, `${format(report.source.events)} events. Same answers. Less work.`, 30, '#edf6f7'),
  ];
  const cards = [
    [indexed.batch_mean_ms.median.toFixed(4) + ' ms', '0.1% time range: median warm batch mean'],
    [(engines[0].cases[0].batch_mean_ms.median / indexed.batch_mean_ms.median).toFixed(0) + 'x', 'vs row scan for this selective case only'],
    [(indexed.result.stats.rows_skipped / report.source.events * 100).toFixed(1) + '%', 'candidate visits skipped; probes counted separately'],
  ];
  cards.forEach(([value, caption], i) => {
    const left = 40 + i * 403;
    parts.push(`<rect x="${left}" y="112" width="389" height="99" rx="9" fill="#102227" stroke="#284046"/>`);
    parts.push(text(left + 18, 155, value, 31, '#65e0c9'), text(left + 18, 185, caption, 12));
  });
  parts.push(text(70, 245, 'Query time (ms, logarithmic)', 14, '#edf6f7'));
  for (let exponent = low; exponent <= high; exponent++) {
    const value = 10 ** exponent, py = y(value);
    parts.push(`<line x1="130" x2="1150" y1="${py}" y2="${py}" stroke="#294048" stroke-dasharray="3 6"/>`);
    parts.push(text(112, py + 5, format(value), 13, '#a8bec4', 'text-anchor="end"'));
  }
  engines.forEach((engine, e) => {
    const points = engine.cases.slice(0, 4).map((c, i) => `${x(i, e)},${y(c.batch_mean_ms.median)}`).join(' ');
    parts.push(`<polyline points="${points}" fill="none" stroke="${colors[e]}" stroke-width="3"/>`);
    engine.cases.slice(0, 4).forEach((c, i) => {
      const px = x(i, e), top = y(c.batch_mean_ms.max), bottom = y(c.batch_mean_ms.min);
      parts.push(`<path d="M${px} ${top}V${bottom}M${px - 5} ${top}H${px + 5}M${px - 5} ${bottom}H${px + 5}" stroke="${colors[e]}" stroke-width="2"/>`);
      parts.push(`<circle cx="${px}" cy="${y(c.batch_mean_ms.median)}" r="5" fill="${colors[e]}"/>`);
    });
    parts.push(`<circle cx="${400 + e * 170}" cy="677" r="5" fill="${colors[e]}"/>`);
    parts.push(text(415 + e * 170, 682, names[e], 15, colors[e]));
  });
  labels.forEach((label, i) => parts.push(text(x(i, 1), 625, label, 14, '#edf6f7', 'text-anchor="middle"')));
  parts.push(text(640, 654, 'Centered fraction of the timestamp span', 14, '#a8bec4', 'text-anchor="middle"'));
  parts.push(text(640, 713, `Median of ${report.settings.samples} batch means; whiskers = min/max batch means, NOT request percentiles.`, 13, '#a8bec4', 'text-anchor="middle"'));
  parts.push('<rect x="40" y="737" width="1200" height="76" rx="9" fill="#102227" stroke="#284046"/>');
  parts.push(text(60, 764, 'NEGATIVE CONTROL / SERVICE-ONLY FILTER', 12, '#edf6f7'));
  parts.push(text(60, 793, `All engines still visit ${format(report.source.events)} candidates.`, 14));
  engines.forEach((engine, i) => parts.push(text(590 + i * 210, 784,
    `${engine.engine}: ${engine.cases[4].batch_mean_ms.median.toFixed(2)} ms`, 16, colors[i])));
  const loads = engines.map(engine => engine.load.ms);
  const validation = inputFormat === 'snapshot' ? 'snapshot integrity checks and source SHA-256' : 'strict JSONL parsing and SHA-256';
  const loading = loads.every(value => Number.isFinite(value) && value > 0)
    ? `Loading: ${(Math.min(...loads) / 1000).toFixed(1)}-${(Math.max(...loads) / 1000).toFixed(1)} seconds per layout, including ${validation}.`
    : 'Loading timings unresolved for at least one layout; see raw report (never inferred as zero).';
  parts.push(text(40, 847, loading, 14));
  parts.push(text(40, 873, `Warm queries only; no loading, charts, HTTP, or browser rendering. ${report.runtime.go_version}, ${report.runtime.os}/${report.runtime.arch}.`, 13));
  parts.push(text(40, 897, 'One shared-host run. Raw samples, exact aggregates, independent oracle, and reproduction commands are published alongside this chart.', 12));
  parts.push('</g></svg>\n');
  return parts.join('\n');
}

try {
  const [input, output, ...extra] = process.argv.slice(2);
  if (!input || !output || extra.length) throw new Error('Usage: node benchmarks/render-chart.mjs <report.json> <new-output.svg>');
  writeFileSync(output, render(JSON.parse(readFileSync(input, 'utf8'))), { flag: 'wx' });
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
