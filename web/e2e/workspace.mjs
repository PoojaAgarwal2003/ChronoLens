import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function workspace() {
  const cache = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'node_modules', '.cache');
  const value = process.env.CHRONOLENS_E2E_DIR;
  const token = process.env.CHRONOLENS_E2E_TOKEN;
  if (!value || !token) throw new Error('Use the Playwright configuration to create a scoped test workspace.');
  const work = resolve(value);
  if (dirname(work) !== cache || !/^chronolens-e2e-[0-9a-f-]{36}$/.test(basename(work))) {
    throw new Error('Refusing test cleanup outside a uniquely scoped frontend cache directory.');
  }
  return { work, cache, token };
}
