// Independent aggregate oracle for the documented epoch-start, 1us uniform fixture.
import { createReadStream } from 'node:fs';
import { createHash } from 'node:crypto';
import { createInterface } from 'node:readline';
import { basename } from 'node:path';

async function main() {
  const [input, countText, ...extra] = process.argv.slice(2);
  const expectedCount = Number(countText);
  if (!input || extra.length || !/^[1-9]\d*$/.test(countText ?? '') ||
      !Number.isSafeInteger(expectedCount)) {
    throw new Error('Usage: node benchmarks/uniform-oracle.mjs <input.jsonl> <event-count>');
  }
  const cases = [1000, 100, 10, 1].map(divisor => {
    const width = Math.floor((expectedCount - 1) / divisor) + 1;
    const from = Math.floor((expectedCount - width) / 2);
    return { name: `${100 / divisor}% time`, from_us: from, to_us: from + width, service: '' };
  });
  cases.push({ name: 'service-only', from_us: 0, to_us: expectedCount, service: 'service-001' });
  const results = cases.map(filter => ({
    filter, count: 0, error_count: 0, duration_sum_us: 0n,
    min_duration_us: null, max_duration_us: null,
  }));
  const stream = createReadStream(input);
  const sha = createHash('sha256');
  let bytes = 0;
  stream.on('data', chunk => { sha.update(chunk); bytes += chunk.length; });
  const lines = createInterface({ input: stream, crlfDelay: Infinity });
  let count = 0;
  const services = new Set();
  try {
    for await (const line of lines) {
      const event = JSON.parse(line);
      if (count >= expectedCount || event.timestamp_us !== count ||
          !Number.isInteger(event.duration_us) || event.duration_us < 100 || event.duration_us > 500000 ||
          ![200, 500].includes(event.status) || !/^service-(00[1-9]|01[0-6])$/.test(event.service)) {
        throw new Error(`Line ${count + 1} does not match the documented uniform fixture contract`);
      }
      count++;
      services.add(event.service);
      for (const result of results) {
        const f = result.filter;
        if (event.timestamp_us < f.from_us || event.timestamp_us >= f.to_us ||
            (f.service && f.service !== event.service)) continue;
        result.count++;
        result.error_count += Number(event.status >= 500);
        result.duration_sum_us += BigInt(event.duration_us);
        result.min_duration_us = Math.min(result.min_duration_us ?? event.duration_us, event.duration_us);
        result.max_duration_us = Math.max(result.max_duration_us ?? event.duration_us, event.duration_us);
      }
    }
  } finally {
    lines.close();
    stream.destroy();
  }
  if (count !== expectedCount) throw new Error(`Expected ${expectedCount} records; read ${count}`);
  console.log(JSON.stringify({
    input: basename(input), events: count, services: [...services].sort(),
    input_bytes: bytes, sha256: sha.digest('hex'),
    cases: results.map(result => ({
      ...result,
      duration_sum_us: result.duration_sum_us.toString(),
      mean_duration_us: result.count ? Number(result.duration_sum_us) / result.count : null,
    })),
  }, null, 2));
}

main().catch(error => { console.error(error.message); process.exitCode = 1; });
