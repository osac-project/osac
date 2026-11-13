import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { useTrimmedInput } from './useTrimmedInput';

const renderTrimmedInput = (initialValue: string | undefined) => {
  const setValue = vi.fn<(value: string) => void>();
  const hook = renderHook(
    ({ value }: { value: string | undefined }) => useTrimmedInput(value, setValue),
    { initialProps: { value: initialValue } },
  );
  return { ...hook, setValue };
};

describe('useTrimmedInput', () => {
  it('keeps the draft visible while passing trimmed user input to the caller', () => {
    const { result, rerender, setValue } = renderTrimmedInput('');

    act(() => result.current.onChange('  '));
    expect(result.current.value).toBe('  ');
    expect(setValue).toHaveBeenLastCalledWith('');

    act(() => result.current.onChange('  production cluster  '));
    expect(setValue).toHaveBeenLastCalledWith('production cluster');
    rerender({ value: 'production cluster' });
    expect(result.current.value).toBe('  production cluster  ');

    act(() => result.current.onBlur());
    expect(result.current.value).toBe('production cluster');
    expect(setValue).toHaveBeenCalledTimes(2);
  });

  it('preserves an untouched supplied value on blur', () => {
    const { result, setValue } = renderTrimmedInput('  prefilled  ');

    expect(result.current.value).toBe('  prefilled  ');
    act(() => result.current.onBlur());
    expect(result.current.value).toBe('  prefilled  ');
    expect(setValue).not.toHaveBeenCalled();
  });

  it('renders an untouched undefined value as empty without writing it back', () => {
    const { result, setValue } = renderTrimmedInput(undefined);

    expect(result.current.value).toBe('');
    act(() => result.current.onBlur());
    expect(result.current.value).toBe('');
    expect(setValue).not.toHaveBeenCalled();
  });

  it('shows an external value change instead of a stale draft', () => {
    const { result, rerender, setValue } = renderTrimmedInput('initial');

    act(() => result.current.onChange('  edited  '));
    rerender({ value: 'edited' });
    expect(result.current.value).toBe('  edited  ');

    rerender({ value: '  external value  ' });
    expect(result.current.value).toBe('  external value  ');
    expect(setValue).toHaveBeenCalledTimes(1);
    expect(setValue).toHaveBeenCalledWith('edited');
  });
});
