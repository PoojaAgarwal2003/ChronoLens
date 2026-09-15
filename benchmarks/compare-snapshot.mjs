import { createHash } from 'node:crypto';
import { closeSync, createReadStream, fstatSync, openSync, readSync } from 'node:fs';

const MAX_EVENTS = 100_000_000;
const MAX_PAYLOAD = 598_016;
const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });
const fields = ['timestamp_us', 'service', 'duration_us', 'status'];
// Reflected Castagnoli polynomial, independently implemented from the Go codec.
const crcTable = Uint32Array.from({ length: 256 }, (_, index) => {
  let crc = index;
  for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0x82f63b78 : 0);
  return crc >>> 0;
});

function crc32c(...buffers) {
  let crc = 0xffffffff;
  for (const buffer of buffers) {
    for (const byte of buffer) crc = (crc >>> 8) ^ crcTable[(crc ^ byte) & 255];
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function require(condition, message) {
  if (!condition) throw new Error(message);
}

function serviceValid(name) {
  // Go strings.TrimSpace uses Unicode White_Space (not JavaScript's BOM rule).
  return typeof name === 'string' && name.isWellFormed() &&
    Buffer.byteLength(name, 'utf8') >= 1 && Buffer.byteLength(name, 'utf8') <= 128 &&
    !/^\p{White_Space}|\p{White_Space}$/u.test(name);
}

function parseEvent(bytes, number) {
  if (bytes.at(-1) === 13) bytes = bytes.subarray(0, -1);
  require(bytes.length <= 4096, `source row ${number}: record exceeds 4096 bytes`);
  const line = decoder.decode(bytes);
  const event = JSON.parse(line, (key, value, context) => {
    if (typeof value === 'number') {
      require(/^-?(?:0|[1-9][0-9]*)$/.test(context.source) && Number.isSafeInteger(value),
        `source row ${number}: ${key} must be a safe integer token; unsupported precision`);
    }
    return value;
  });
  require(event && !Array.isArray(event) && typeof event === 'object' &&
    Object.keys(event).length === 4 && fields.every(key => Object.hasOwn(event, key)),
  `source row ${number}: expected exactly timestamp_us, service, duration_us, status`);
  // Tokenize whole strings first so colons/escaped quotes inside a service cannot
  // masquerade as field names. JSON.parse alone would silently accept duplicates.
  const tokens = line.match(/"(?:\\.|[^"\\])*"|[{}:,\[\]]|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null/g);
  require(tokens.filter(token => token === ':').length === 4,
    `source row ${number}: duplicate or nested fields`);
  require(Number.isSafeInteger(event.timestamp_us) && event.timestamp_us >= 0,
    `source row ${number}: timestamp_us must be a nonnegative safe integer`);
  require(serviceValid(event.service), `source row ${number}: invalid service`);
  require(Number.isInteger(event.duration_us) && event.duration_us >= 0 && event.duration_us <= 0xffffffff,
    `source row ${number}: invalid duration_us`);
  require(Number.isInteger(event.status) && event.status >= 100 && event.status <= 599,
    `source row ${number}: invalid status`);
  return event;
}

async function* sourceEvents(path, digest, state) {
  const stream = createReadStream(path, { highWaterMark: 64 * 1024 });
  let pending = Buffer.alloc(0), rows = 0;
  try {
    for await (const chunk of stream) {
      digest.update(chunk);
      state.bytes += chunk.length;
      let start = 0;
      for (let end = chunk.indexOf(10); end !== -1; end = chunk.indexOf(10, start)) {
        require(pending.length + end - start <= 4097, 'source record exceeds 4096 bytes');
        const line = pending.length ? Buffer.concat([pending, chunk.subarray(start, end)]) : chunk.subarray(start, end);
        pending = Buffer.alloc(0);
        require(++rows <= MAX_EVENTS, 'source event limit exceeded');
        yield parseEvent(line, rows);
        start = end + 1;
      }
      require(pending.length + chunk.length - start <= 4097, 'source record exceeds 4096 bytes');
      pending = Buffer.concat([pending, chunk.subarray(start)]);
    }
    if (pending.length) {
      require(++rows <= MAX_EVENTS, 'source event limit exceeded');
      yield parseEvent(pending, rows);
    }
  } finally {
    stream.destroy();
  }
}

async function compare(sourcePath, snapshotPath, expected) {
  const sourceHash = createHash('sha256'), snapshotHash = createHash('sha256'), prefixHash = createHash('sha256');
  const sourceState = { bytes: 0 };
  const source = sourceEvents(sourcePath, sourceHash, sourceState);
  let fd, snapshotBytes = 0, events = 0, blocks = 0, previous = 0n;
  const services = new Set();
  try {
    fd = openSync(snapshotPath, 'r');
    require(fstatSync(fd).isFile(), 'snapshot must be a regular file');
    const read = (length, prefix = true) => {
      require(Number.isInteger(length) && length >= 0 && length <= MAX_PAYLOAD, 'invalid read allocation');
      const bytes = Buffer.allocUnsafe(length);
      let offset = 0;
      while (offset < length) {
        const count = readSync(fd, bytes, offset, length - offset, null);
        require(count > 0, 'truncated snapshot: unexpected EOF');
        offset += count;
      }
      snapshotBytes += length;
      snapshotHash.update(bytes);
      if (prefix) prefixHash.update(bytes);
      return bytes;
    };
    const header = read(16);
    require(header.subarray(0, 8).equals(Buffer.from('CHRONOSN')), 'invalid snapshot magic');
    require(header.readUInt16LE(8) === 1, 'unsupported snapshot version');
    require(header.readUInt16LE(10) === 0 && header.readUInt32LE(12) === 0, 'unsupported flags or reserved bits');
    for (;;) {
      const marker = read(4);
      if (marker.equals(Buffer.from('END1'))) {
        const totals = read(16);
        require(totals.readBigUInt64LE(0) === BigInt(events) && totals.readBigUInt64LE(8) === BigInt(blocks),
          'trailer totals mismatch');
        require(read(32, false).equals(prefixHash.digest()), 'snapshot SHA-256 checksum mismatch');
        require(readSync(fd, Buffer.alloc(1), 0, 1, null) === 0, 'trailing bytes after snapshot');
        require(events === expected, `event count mismatch: expected ${expected}, decoded ${events}`);
        require((await source.next()).done, 'source contains extra events');
        return {
          events, blocks, source_sha256: sourceHash.digest('hex'), snapshot_sha256: snapshotHash.digest('hex'),
          source_bytes: sourceState.bytes, snapshot_bytes: snapshotBytes, records_equal: true,
          snapshot_version: 1, crc32c_valid: true, snapshot_prefix_sha256_valid: true,
        };
      }
      require(marker.equals(Buffer.from('BLK1')), 'invalid snapshot block marker');
      const frame = Buffer.concat([marker, read(12)]);
      const rows = frame.readUInt32LE(4), names = frame.readUInt32LE(8), length = frame.readUInt32LE(12);
      require(rows >= 1 && rows <= 4096, 'block row count outside 1..4096');
      require(names >= 1 && names <= rows, 'block dictionary count outside 1..rows');
      require(length >= rows * 16 + names * 3 && length <= rows * 16 + names * 130 && length <= MAX_PAYLOAD,
        'invalid block payload length');
      require(rows <= expected - events, 'snapshot exceeds expected event count');
      const payload = read(length), checksum = read(4);
      require(checksum.readUInt32LE(0) === crc32c(frame, payload), `block ${blocks + 1}: CRC32C checksum mismatch`);
      const dictionaryEnd = length - rows * 16, dictionary = [], seen = new Set();
      let offset = 0;
      for (let i = 0; i < names; i++) {
        require(dictionaryEnd - offset >= 2, 'missing dictionary name length');
        const size = payload.readUInt16LE(offset);
        offset += 2;
        require(size >= 1 && size <= 128 && size <= dictionaryEnd - offset, 'invalid dictionary name length');
        const name = decoder.decode(payload.subarray(offset, offset + size));
        offset += size;
        require(serviceValid(name), 'invalid dictionary service');
        require(!seen.has(name), 'duplicate dictionary service');
        seen.add(name);
        services.add(name);
        require(services.size <= 65536, 'global service limit exceeded');
        dictionary.push(name);
      }
      require(offset === dictionaryEnd, 'unexpected dictionary bytes');
      for (let i = 0; i < rows; i++) {
        const timestamp = payload.readBigInt64LE(offset + i * 8);
        const id = payload.readUInt16LE(offset + rows * 8 + i * 2);
        const duration = payload.readUInt32LE(offset + rows * 10 + i * 4);
        const status = payload.readUInt16LE(offset + rows * 14 + i * 2);
        require(id < names, 'service ID outside dictionary');
        require(timestamp >= previous && timestamp >= 0n, 'snapshot timestamps must be nonnegative and nondecreasing');
        require(timestamp <= BigInt(Number.MAX_SAFE_INTEGER), 'snapshot timestamp exceeds safe integer precision supported by this verifier');
        require(status >= 100 && status <= 599, 'snapshot status outside 100..599');
        const next = await source.next();
        require(!next.done, `source ended before event ${events + 1}`);
        const actual = [Number(timestamp), dictionary[id], duration, status];
        fields.forEach((field, index) => require(next.value[field] === actual[index],
          `event ${events + 1}: ${field} differs between source and snapshot`));
        previous = timestamp;
        events++;
      }
      blocks++;
    }
  } finally {
    try {
      await source.return();
    } finally {
      if (fd !== undefined) closeSync(fd);
    }
  }
}

try {
  const [source, snapshot, count, ...extra] = process.argv.slice(2);
  require(source && snapshot && count && !extra.length,
    'Usage: node benchmarks/compare-snapshot.mjs <source.jsonl> <snapshot.clens> <expected-events>');
  require(/^[1-9][0-9]*$/.test(count) && Number.isSafeInteger(Number(count)) && Number(count) <= MAX_EVENTS,
    'expected-events must be a positive integer <= 100000000');
  console.log(JSON.stringify(await compare(source, snapshot, Number(count))));
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
