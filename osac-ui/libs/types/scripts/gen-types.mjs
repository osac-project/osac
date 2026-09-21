import { readdirSync, unlinkSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const typesDirectory = dirname(dirname(fileURLToPath(import.meta.url)));
const sourceDirectory = join(typesDirectory, 'src');

const removeGeneratedBindings = (directory) => {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const entryPath = join(directory, entry.name);

    if (entry.isDirectory()) {
      removeGeneratedBindings(entryPath);
    } else if (entry.isFile() && entry.name.endsWith('_pb.ts')) {
      unlinkSync(entryPath);
    }
  }
};

try {
  removeGeneratedBindings(sourceDirectory);

  const result = spawnSync('buf', ['generate'], {
    cwd: typesDirectory,
    stdio: 'inherit',
  });

  if (result.error) {
    throw result.error;
  }

  process.exitCode = result.status ?? 1;
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
