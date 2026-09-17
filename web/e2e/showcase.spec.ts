import { expect, test } from '@playwright/test';
// @ts-expect-error Node capture helpers are exercised directly without a build step.
import { captureName, validateVideo } from '../showcase/validation.mjs';

test('capture names cannot escape the dedicated media directory', () => {
  expect(captureName('final-showcase')).toBe('final-showcase');
  for (const value of ['', '../other', '..\\other', 'C:\\data', '/data', '-hidden', 'a'.repeat(81)]) {
    expect(() => captureName(value)).toThrow();
  }
});

test('recording validation requires a bounded decoded 30–90 second video', () => {
  const good = { duration: 42, width: 1280, height: 900, decodedFrames: 12, bytes: 500000 };
  expect(() => validateVideo(good)).not.toThrow();
  for (const patch of [{ duration: Infinity }, { duration: NaN }, { duration: 29.9 }, { duration: 90.1 },
    { width: 640 }, { height: 720 }, { decodedFrames: 0 }, { bytes: 0 }, { bytes: 20000001 }]) {
    expect(() => validateVideo({ ...good, ...patch })).toThrow();
  }
});
