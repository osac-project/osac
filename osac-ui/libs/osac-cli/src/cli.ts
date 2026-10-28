import { expect } from '@playwright/test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

export interface OsacCommandResult {
  exitCode: number;
  stderr: string;
  stdout: string;
}

const cliPath = process.env.OSAC_CLI_PATH ?? 'osac';

const cliPrefix = process.env.OSAC_CLI_CONFIG ? ['--config', process.env.OSAC_CLI_CONFIG] : [];

export const runOsacCommand = async (args: readonly string[]): Promise<OsacCommandResult> => {
  try {
    const result = await execFileAsync(cliPath, [...cliPrefix, ...args], {
      env: { ...process.env, NO_COLOR: '1' },
      maxBuffer: 1024 * 1024,
      timeout: 120_000,
    });

    return { exitCode: 0, stderr: result.stderr, stdout: result.stdout };
  } catch (error: unknown) {
    const commandError = error as NodeJS.ErrnoException & {
      stderr?: string;
      stdout?: string;
    };

    if (commandError.code === 'ENOENT') {
      throw new Error(
        `Unable to run the osac CLI '${cliPath}'. Set OSAC_CLI_PATH to an installed CLI binary.`,
      );
    }

    return {
      exitCode: typeof commandError.code === 'number' ? commandError.code : 1,
      stderr: commandError.stderr ?? commandError.message,
      stdout: commandError.stdout ?? '',
    };
  }
};

export const expectOsacCommandSucceeded = (
  result: OsacCommandResult,
  description: string,
): void => {
  expect(
    result.exitCode,
    `${description} failed.\nstdout: ${result.stdout}\nstderr: ${result.stderr}`,
  ).toBe(0);
};
