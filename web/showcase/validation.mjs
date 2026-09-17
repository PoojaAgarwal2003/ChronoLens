export function captureName(value) {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9-]{0,79}$/.test(value)) {
    throw new Error('Capture name must be 1–80 letters, digits, or hyphens; no paths.');
  }
  return value;
}

export function validateVideo({ duration, width, height, decodedFrames, bytes }) {
  if (!Number.isFinite(duration) || duration < 30 || duration > 90) {
    throw new Error(`Expected a 30–90 second recording; got ${duration}`);
  }
  if (width !== 1280 || height !== 900 || decodedFrames < 1 || bytes < 1000 || bytes > 20_000_000) {
    throw new Error('Recording failed dimension, decode, or size checks.');
  }
}
