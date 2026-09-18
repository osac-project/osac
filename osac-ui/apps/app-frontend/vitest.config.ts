import react from '@vitejs/plugin-react';
import { resolve } from 'path';
import { defineConfig } from 'vitest/config';

const uiComponentsSrc = resolve(__dirname, '../../libs/ui-components/src');
const appNodeModules = resolve(__dirname, 'node_modules');

const testDependencyAliases = [
  '@testing-library/react',
  '@testing-library/user-event',
  '@testing-library/jest-dom',
].map((pkg) => ({
  find: pkg,
  replacement: resolve(appNodeModules, pkg),
}));

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: [
      {
        find: /^@osac\/ui-components\/(.+)$/,
        replacement: `${uiComponentsSrc}/$1`,
      },
      ...testDependencyAliases,
    ],
  },
  test: {
    environment: 'jsdom',
    passWithNoTests: true,
    // Explicit reporters list opts out of Vitest's automatic 'github-actions'
    // reporter (which writes a "Vitest Test Report" job summary) -- Vitest
    // only auto-adds it when `reporters` is left unset.
    reporters: ['default'],
    setupFiles: ['./src/test-setup.ts'],
    include: [
      'src/**/*.{test,spec}.{ts,tsx}',
      '../../libs/ui-components/src/**/*.{test,spec}.{ts,tsx}',
    ],
    server: {
      deps: {
        inline: ['@testing-library/react', '@testing-library/user-event', '@testing-library/jest-dom'],
      },
    },
  },
});
