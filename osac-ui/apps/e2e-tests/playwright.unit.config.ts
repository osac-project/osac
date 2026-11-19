import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  outputDir: '../playwright/test-results/helper-tests',
  reporter: 'line',
  use: { baseURL: 'http://vm-deletion.test' },
});
