import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

afterEach(() => {
  cleanup();
});

class ResizeObserverMock {
  observe() {
    return undefined;
  }
  unobserve() {
    return undefined;
  }
  disconnect() {
    return undefined;
  }
}

globalThis.ResizeObserver = ResizeObserverMock;

// Node can expose a global localStorage when it is started with an invalid
// --localstorage-file flag. Keep Vitest tests on a functional browser-like
// storage implementation regardless of the Node runtime configuration.
const createMemoryStorage = (): Storage => {
  const values = new Map<string, string>();

  return {
    get length() {
      return values.size;
    },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => Array.from(values.keys())[index] ?? null,
    removeItem: (key) => values.delete(key),
    setItem: (key, value) => values.set(key, String(value)),
  };
};

const storage = (() => {
  try {
    const candidate = window.localStorage;
    candidate.getItem('__vitest_storage_probe__');
    return candidate;
  } catch {
    return createMemoryStorage();
  }
})();

Object.defineProperty(window, 'localStorage', { configurable: true, value: storage });
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage });

// PatternFly Wizard shows the desktop footer (Back / Next / Cancel) at md+ breakpoints.
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: /min-width:\s*(768|992|1200)px/.test(query),
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  }),
});
