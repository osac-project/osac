import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query';
import { screen } from '@testing-library/react';

/**
 * Resolves the typeahead combobox input for a `ResourceSelectField`/`TypeaheadSelectField` from
 * its dropdown-arrow toggle button. The field's accessible label lands on that button, not the
 * combobox input itself (a PatternFly `TypeaheadSelect` quirk), so this walks from the button up
 * to the shared `MenuToggle` container and back down to the input.
 */
const resolveResourceSelectInput = (toggleButton: HTMLElement): HTMLInputElement => {
  const container = toggleButton.closest('.pf-v6-c-menu-toggle');
  const input = container?.querySelector<HTMLInputElement>('input[role="combobox"]');
  if (!input) {
    throw new Error('No combobox input found for the given toggle button');
  }
  return input;
};

/** Finds a single `ResourceSelectField`/`TypeaheadSelectField` combobox input by its form label. */
export const getResourceSelectInput = (label: string | RegExp): HTMLInputElement =>
  resolveResourceSelectInput(screen.getByLabelText(label));

/**
 * Finds every `ResourceSelectField`/`TypeaheadSelectField` combobox input sharing a form label
 * (e.g. one per row in a repeatable field array), in document order.
 */
export const getAllResourceSelectInputs = (label: string | RegExp): HTMLInputElement[] =>
  screen.getAllByLabelText(label).map(resolveResourceSelectInput);

export const mockQueryResult = <T>(
  overrides: Partial<UseQueryResult<T, unknown>> = {},
): UseQueryResult<T, unknown> =>
  ({
    data: undefined,
    isLoading: false,
    error: null,
    ...overrides,
  }) as UseQueryResult<T, unknown>;

// Constraint requires `any` — UseMutationResult type params are contravariant
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyMutationResult = UseMutationResult<any, any, any, any>;

export const mockMutationResult = <T extends AnyMutationResult>(overrides: Partial<T> = {}): T =>
  ({
    mutateAsync: overrides.mutateAsync,
    ...overrides,
  }) as T;
