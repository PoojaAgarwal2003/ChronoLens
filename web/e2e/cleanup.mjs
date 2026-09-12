import { readFile, stat, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout } from 'node:timers/promises';
import { workspace } from './workspace.mjs';

export default async function cleanup() {
  const { work, token } = workspace();
  try {
    const owner = await readFile(join(work, 'owner'), 'utf8');
    if (owner !== token) throw new Error('Refusing to stop a workspace owned by another test run.');
    await writeFile(join(work, 'shutdown'), '');
  } catch (error) {
    if (error.code === 'ENOENT') return;
    throw error;
  }
  for (let attempt = 0; attempt < 120; attempt++) {
    try { await stat(work); } catch (error) {
      if (error.code === 'ENOENT') return;
      throw error;
    }
    await setTimeout(50);
  }
  throw new Error('The owned test server did not stop cleanly; refusing unsafe PID-based cleanup.');
}
