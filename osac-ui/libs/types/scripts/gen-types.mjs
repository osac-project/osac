import { mkdtempSync, readFileSync, readdirSync, rmSync, unlinkSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const typesDirectory = dirname(dirname(fileURLToPath(import.meta.url)));
const sourceDirectory = join(typesDirectory, 'src');
const bufConfigTemplatePath = join(typesDirectory, 'buf.gen.yaml.template');
const bufApiVersionPath = join(typesDirectory, '.buf-api-version');
const apiVersionPattern = /^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$/;

const readApiVersion = () => {
  const lines = readFileSync(bufApiVersionPath, 'utf8').split(/\r?\n/);
  if (lines.at(-1) === '') {
    lines.pop();
  }

  const [version] = lines;
  if (lines.length !== 1 || !apiVersionPattern.test(version)) {
    throw new Error(`Expected one stable API version in ${bufApiVersionPath}`);
  }

  return version;
};

const renderBufConfig = () => {
  const template = readFileSync(bufConfigTemplatePath, 'utf8');
  const placeholder = '__OSAC_API_VERSION__';
  const placeholderCount = template.split(placeholder).length - 1;

  if (placeholderCount !== 2) {
    throw new Error(`Expected two ${placeholder} placeholders in ${bufConfigTemplatePath}`);
  }

  return template.replaceAll(placeholder, readApiVersion());
};

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

let tempDirectory;
try {
  tempDirectory = mkdtempSync(join(tmpdir(), 'osac-buf-generate-'));
  const bufConfigPath = join(tempDirectory, 'buf.gen.yaml');
  writeFileSync(bufConfigPath, renderBufConfig());
  removeGeneratedBindings(sourceDirectory);

  const result = spawnSync('buf', ['generate', '--template', bufConfigPath], {
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
} finally {
  if (tempDirectory) {
    rmSync(tempDirectory, { force: true, recursive: true });
  }
}
