import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import { useUserPreferences } from './use-user-preferences';

const TEST_PREF_KEY = 'test-user-preference-key';

describe('useUserPreferences', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('updates all hook instances that share the same key', () => {
    const first = renderHook(() => useUserPreferences(TEST_PREF_KEY));
    const second = renderHook(() => useUserPreferences(TEST_PREF_KEY));

    expect(first.result.current[0]).toBeNull();
    expect(second.result.current[0]).toBeNull();

    act(() => {
      first.result.current[1]('list');
    });

    expect(first.result.current[0]).toBe('list');
    expect(second.result.current[0]).toBe('list');
    expect(localStorage.getItem(TEST_PREF_KEY)).toBe('list');
  });

  it('updates when localStorage changes from another document context', () => {
    const { result } = renderHook(() => useUserPreferences(TEST_PREF_KEY));

    act(() => {
      localStorage.setItem(TEST_PREF_KEY, 'cards');
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: TEST_PREF_KEY,
          newValue: 'cards',
          storageArea: localStorage,
        }),
      );
    });

    expect(result.current[0]).toBe('cards');
  });
});
