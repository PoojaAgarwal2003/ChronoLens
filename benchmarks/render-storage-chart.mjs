import { readFileSync, writeFileSync } from 'node:fs';
import { isDeepStrictEqual } from 'node:util';

const names = ['row', 'columnar', 'indexed'];
const escape = value => String(value).replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;',
}[character]));
const format = value => value.toLocaleString('en-US', { maximumFractionDigits: 6 });

function require(condition, message) {
  if (!condition) throw new Error(message);
}

function integer(value, min, max, label) {
  require(Number.isSafeInteger(value) && value >= min && value <= max, `invalid ${label}`);
}

function positive(value, label) {
  require(typeof value === 'number' && Number.isFinite(value) && value > 0, `requires positive, resolved ${label}`);
}

function readReport(path) {
  return JSON.parse(readFileSync(path, 'utf8'), (key, value, context) => {
    if (typeof value === 'number' && key === 'duration_sum_us') {
      require(/^[0-9]+$/.test(context.source), 'aggregate duration_sum_us must be an exact unsigned integer token');
    }
    // Aggregated uint64 duration sums can exceed Number's exact integer range.
    if (typeof value === 'number' && /^-?[0-9]+$/.test(context.source) && !Number.isSafeInteger(value)) {
      return BigInt(context.source);
    }
    return value;
  });
}

function validateAggregate(aggregate, events) {
  require(aggregate && typeof aggregate === 'object', 'missing aggregate');
  integer(aggregate.count, 0, events, 'aggregate count');
  integer(aggregate.error_count, 0, aggregate.count, 'aggregate error_count');
  const sum = aggregate.duration_sum_us;
  require((typeof sum === 'bigint' || Number.isSafeInteger(sum)) &&
    BigInt(sum) >= 0n && BigInt(sum) <= BigInt(aggregate.count) * 0xffffffffn, 'invalid aggregate duration_sum_us');
  if (aggregate.count === 0) {
    require(sum === 0 && aggregate.mean_duration_us === null &&
      aggregate.min_duration_us === null && aggregate.max_duration_us === null, 'invalid empty aggregate');
  } else {
    integer(aggregate.min_duration_us, 0, 0xffffffff, 'aggregate minimum duration');
    integer(aggregate.max_duration_us, aggregate.min_duration_us, 0xffffffff, 'aggregate maximum duration');
    require(Number.isFinite(aggregate.mean_duration_us) &&
      aggregate.mean_duration_us === Number(sum) / aggregate.count, 'invalid aggregate mean duration');
    require(BigInt(sum) >= BigInt(aggregate.min_duration_us) * BigInt(aggregate.count) &&
      BigInt(sum) <= BigInt(aggregate.max_duration_us) * BigInt(aggregate.count), 'aggregate sum outside duration bounds');
  }
}

function validate(report, inputFormat) {
  require(report?.schema_version === 1 && report.source?.input_format === inputFormat,
    `expected schema-v1 source.input_format exactly ${inputFormat}`);
  const source = report.source;
  integer(source.events, 1, 100_000_000, 'source events');
  integer(source.bytes, 1, Number.MAX_SAFE_INTEGER, 'source bytes');
  integer(source.services, 1, Math.min(source.events, 65536), 'source services');
  integer(source.min_us, 0, Number.MAX_SAFE_INTEGER, 'source min_us');
  integer(source.max_us, source.min_us, Number.MAX_SAFE_INTEGER, 'source max_us');
  integer(source.time_span_us, 1, Number.MAX_SAFE_INTEGER, 'source time_span_us');
  require(source.time_span_us === source.max_us - source.min_us + 1, 'invalid source time span');
  require(typeof source.sha256 === 'string' && /^[a-f0-9]{64}$/.test(source.sha256), 'invalid source SHA-256');
  require(Array.isArray(report.cases) && report.cases.length > 0, 'missing case filters');
  const caseNames = new Set();
  for (const item of report.cases) {
    require(typeof item?.name === 'string' && item.name.length > 0 && !caseNames.has(item.name), 'invalid or duplicate case name');
    caseNames.add(item.name);
    const filter = item.filter;
    require(filter && typeof filter === 'object', 'missing case filter');
    integer(filter.from_us, 0, Number.MAX_SAFE_INTEGER, 'filter from_us');
    if (filter.to_us !== null) integer(filter.to_us, filter.from_us, Number.MAX_SAFE_INTEGER, 'filter to_us');
    require(typeof filter.service === 'string' && filter.service.isWellFormed() &&
      Buffer.byteLength(filter.service) <= 128 && !/^\p{White_Space}|\p{White_Space}$/u.test(filter.service),
    'invalid filter service');
    require(filter.status === 0 || (Number.isInteger(filter.status) && filter.status >= 100 && filter.status <= 599),
      'invalid filter status');
  }
  require(Array.isArray(report.engines) && report.engines.length === 3 &&
    new Set(report.engines.map(engine => engine.engine)).size === 3, 'expected exactly three unique engines');
  const engines = names.map(name => report.engines.find(engine => engine.engine === name));
  require(engines.every(Boolean), 'expected row, columnar, indexed engines');
  for (const engine of engines) {
    positive(engine.load?.ms, `${engine.engine} loading timing`);
    require(!Object.hasOwn(engine.load, 'stable_window') || engine.load.stable_window === true, 'unstable loading timing');
    require(engine.load.sha256 === source.sha256 && engine.load.bytes === source.bytes, 'load provenance differs from source');
    integer(engine.heap?.delta_heap_alloc_bytes, -Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER, 'heap delta');
    require(Array.isArray(engine.cases) && engine.cases.length === report.cases.length, 'engine case count mismatch');
    engine.cases.forEach((item, index) => {
      require(item.name === report.cases[index].name, 'engine case order/name mismatch');
      validateAggregate(item.result?.aggregate, source.events);
    });
  }
  return engines;
}

function render(jsonl, snapshot) {
  const layouts = [validate(jsonl, 'jsonl'), validate(snapshot, 'snapshot')];
  for (const field of ['events', 'services', 'min_us', 'max_us', 'time_span_us']) {
    require(jsonl.source[field] === snapshot.source[field], `source ${field} differs across formats`);
  }
  require(isDeepStrictEqual(jsonl.cases, snapshot.cases), 'case filters/descriptors differ across formats');
  for (const engines of layouts) {
    for (const engine of engines) {
      engine.cases.forEach((item, index) => require(
        isDeepStrictEqual(item.result.aggregate, layouts[0][0].cases[index].result.aggregate),
        `aggregate differs for ${engine.engine}/${item.name}`));
    }
  }
  const colors = ['#315bd6', '#007e72'];
  const formats = ['JSONL', 'snapshot'];
  const seconds = layouts.flatMap(engines => engines.map(engine => engine.load.ms / 1000));
  seconds.forEach(value => positive(value, 'seconds after conversion'));
  const maximum = Math.max(...seconds);
  const step = 10 ** Math.floor(Math.log10(maximum));
  const ceiling = Math.ceil(maximum / step) * step;
  require(Number.isFinite(ceiling) && ceiling > 0, 'load axis exceeds numeric bounds');
  const left = 210, width = 730, baseline = 637;
  const x = value => left + value / ceiling * width;
  const text = (px, py, content, size = 14, fill = '#526171', extra = '') =>
    `<text x="${px}" y="${py}" font-size="${size}" fill="${fill}" ${extra}>${escape(content)}</text>`;
  const measured = value => value === 0 || value >= 0.000001 ? format(value) : value.toExponential(4);
  const parts = [
    '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="900" viewBox="0 0 1200 900" role="img" aria-labelledby="title desc">',
    '<title id="title">ChronoLens: JSONL versus snapshot loading by engine</title>',
    '<desc id="desc">Six observed load times, paired by row, columnar, and indexed engine, on a linear zero-based seconds axis. Source file sizes are shown separately. One observed load per layout; not p95/cold-disk. No engine averaging or warm query timings. JSONL parsing and source hashing differ from snapshot decoding with block CRC32C, embedded prefix SHA-256, and whole-file source hashing. Conversion cost is not included.</desc>',
    '<rect width="1200" height="900" rx="18" fill="#f4f7fb"/>',
    '<g font-family="Arial, Helvetica, sans-serif">',
    text(42, 42, 'CHRONOLENS / STORAGE & LOADING', 12, '#007e72', 'letter-spacing="2"'),
    text(42, 84, 'Same events. Two storage formats.', 32, '#17283d'),
    text(42, 115, `${format(jsonl.source.events)} events | ${format(jsonl.source.services)} services | exact reported aggregates agree across all engines`, 15),
  ];
  [jsonl, snapshot].forEach((report, index) => {
    const px = 42 + index * 566;
    parts.push(`<rect x="${px}" y="145" width="550" height="96" rx="10" fill="#ffffff" stroke="#d8e0ec"/>`);
    parts.push(text(px + 20, 173, `${formats[index]} / SOURCE FILE`, 12, colors[index], 'letter-spacing="1"'));
    parts.push(text(px + 20, 211, `${format(report.source.bytes)} bytes`, 26, '#17283d'));
  });
  parts.push(text(42, 277, 'Observed loading time (seconds, linear; zero baseline)', 17, '#17283d'));
  formats.forEach((label, index) => {
    parts.push(`<rect x="${800 + index * 153}" y="262" width="18" height="18" rx="3" fill="${colors[index]}"/>`);
    parts.push(text(827 + index * 153, 277, label, 14, '#17283d'));
  });
  for (let tick = 0; tick <= 5; tick++) {
    const value = ceiling * (tick / 5), px = x(value);
    parts.push(`<line x1="${px}" x2="${px}" y1="307" y2="${baseline}" stroke="#d8e0ec"/>`);
    parts.push(text(px, baseline + 23, measured(value), 13, '#526171', 'text-anchor="middle"'));
  }
  names.forEach((name, index) => {
    const top = 323 + index * 106;
    parts.push(text(178, top + 33, name, 19, '#17283d', 'text-anchor="end"'));
    layouts.forEach((engines, inputIndex) => {
      const ms = engines[index].load.ms, value = ms / 1000, py = top + inputIndex * 35;
      parts.push(`<rect class="load-bar" data-engine="${name}" data-format="${formats[inputIndex].toLowerCase()}" data-load-ms="${ms}" x="${left}" y="${py}" width="${x(value) - left}" height="26" rx="3" fill="${colors[inputIndex]}"/>`);
      parts.push(text(x(value) + 10, py + 19, `${measured(value)} s`, 14, colors[inputIndex]));
    });
  });
  parts.push(text(42, 705, 'one observed load per layout; not p95/cold-disk', 17, '#17283d'));
  parts.push(text(42, 732, 'Three engine-specific pairs, not three replicates. Warm queries and browser rendering are not plotted.', 14));
  parts.push('<line x1="42" x2="1158" y1="755" y2="755" stroke="#d8e0ec"/>');
  parts.push(text(42, 783, 'JSONL load: strict record parsing + whole-file source SHA-256.', 14, colors[0]));
  parts.push(text(42, 809, 'Snapshot load: decode + block CRC32C + embedded prefix SHA-256 + whole-file source SHA-256.', 14, colors[1]));
  parts.push(text(42, 841, 'Conversion cost not included. File bytes are storage sizes, not retained heap or transfer measurements.', 14));
  parts.push(text(42, 867, 'Aggregate agreement is not event-level parity; use compare-snapshot.mjs for an independent, full-record check.', 12));
  parts.push('</g></svg>\n');
  return parts.join('\n');
}

try {
  const [jsonl, snapshot, output, ...extra] = process.argv.slice(2);
  require(jsonl && snapshot && output && !extra.length,
    'Usage: node benchmarks/render-storage-chart.mjs <jsonl-report.json> <snapshot-report.json> <new-output.svg>');
  writeFileSync(output, render(readReport(jsonl), readReport(snapshot)), { flag: 'wx' });
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
