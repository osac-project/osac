import * as React from 'react';

const preferenceListeners = new Map<string, Set<() => void>>();

const subscribeToPreferenceKey = (userPreferencesKey: string, onStoreChange: () => void) => {
  let listeners = preferenceListeners.get(userPreferencesKey);
  if (!listeners) {
    listeners = new Set();
    preferenceListeners.set(userPreferencesKey, listeners);
  }
  listeners.add(onStoreChange);

  const onStorage = (event: StorageEvent) => {
    if (event.key === userPreferencesKey) {
      onStoreChange();
    }
  };
  window.addEventListener('storage', onStorage);

  return () => {
    listeners?.delete(onStoreChange);
    if (listeners?.size === 0) {
      preferenceListeners.delete(userPreferencesKey);
    }
    window.removeEventListener('storage', onStorage);
  };
};

const notifyPreferenceListeners = (userPreferencesKey: string) => {
  preferenceListeners.get(userPreferencesKey)?.forEach((listener) => {
    listener();
  });
};

const readUserPreference = (userPreferencesKey: string): string | null => {
  try {
    return localStorage.getItem(userPreferencesKey);
  } catch {
    return null;
  }
};

export const useUserPreferences = (
  userPreferencesKey: string,
): [string | null, (value: string) => void] => {
  const userPreferences = React.useSyncExternalStore(
    (onStoreChange) => subscribeToPreferenceKey(userPreferencesKey, onStoreChange),
    () => readUserPreference(userPreferencesKey),
    () => null,
  );

  const setUserPreferencesState = React.useCallback(
    (value: string) => {
      try {
        localStorage.setItem(userPreferencesKey, value);
      } catch {
        // ignore
      }

      notifyPreferenceListeners(userPreferencesKey);
    },
    [userPreferencesKey],
  );

  return [userPreferences, setUserPreferencesState];
};
