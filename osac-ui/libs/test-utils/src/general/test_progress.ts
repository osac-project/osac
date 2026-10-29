import { test } from '@playwright/test';

/** Write a prefixed, non-sensitive progress message for the test report. */
export const logProgress = (flow: string, message: string): void => {
  test.info().annotations.push({
    type: 'progress',
    description: `[${flow}] ${message}`,
  });
};

/** Add a named Playwright report step with matching start/completion logs. */
export const testStep = async <T>(
  flow: string,
  name: string,
  action: () => Promise<T>,
): Promise<T> => {
  logProgress(flow, `Starting: ${name}`);
  return test.step(name, async () => {
    const result = await action();
    logProgress(flow, `Completed: ${name}`);
    return result;
  });
};
