import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './helper-tests',
  outputDir: '../playwright/test-results/helper-tests',
  reporter: 'line',
  use: { baseURL: 'http://vm-deletion.test' },
});
