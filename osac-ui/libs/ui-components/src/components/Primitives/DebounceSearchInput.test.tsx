import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import DebounceSearchInput from './DebounceSearchInput';

describe('DebounceSearchInput', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('updates the visible value immediately while debouncing onChange', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(
      <DebounceSearchInput value="" onChange={onChange} aria-label="Search" debounceMs={300} />,
    );

    const input = screen.getByRole('textbox', { name: 'Search' });
    await user.type(input, 'ab');

    expect(input).toHaveValue('ab');
    expect(onChange).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(300);

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenLastCalledWith('ab');
  });

  it('syncs the input when the controlled value changes externally', () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <DebounceSearchInput value="alpha" onChange={onChange} aria-label="Search" />,
    );

    expect(screen.getByRole('textbox', { name: 'Search' })).toHaveValue('alpha');

    rerender(<DebounceSearchInput value="" onChange={onChange} aria-label="Search" />);

    expect(screen.getByRole('textbox', { name: 'Search' })).toHaveValue('');
  });

  it('does not apply a pending debounced change after the controlled value clears externally', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const { rerender } = render(
      <DebounceSearchInput
        value="committed"
        onChange={onChange}
        aria-label="Search"
        debounceMs={300}
      />,
    );

    await user.clear(screen.getByRole('textbox', { name: 'Search' }));
    await user.type(screen.getByRole('textbox', { name: 'Search' }), 'stale');
    expect(onChange).not.toHaveBeenCalled();

    rerender(
      <DebounceSearchInput value="" onChange={onChange} aria-label="Search" debounceMs={300} />,
    );

    await vi.advanceTimersByTimeAsync(300);

    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Search' })).toHaveValue('');
  });

  it('does not apply a pending debounced change after resetKey changes without a new value', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const { rerender } = render(
      <DebounceSearchInput
        value=""
        onChange={onChange}
        aria-label="Search"
        debounceMs={300}
        resetKey={0}
      />,
    );

    await user.type(screen.getByRole('textbox', { name: 'Search' }), 'stale');
    expect(onChange).not.toHaveBeenCalled();

    rerender(
      <DebounceSearchInput
        value=""
        onChange={onChange}
        aria-label="Search"
        debounceMs={300}
        resetKey={1}
      />,
    );

    await vi.advanceTimersByTimeAsync(300);

    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Search' })).toHaveValue('');
  });

  it('clears immediately and cancels a pending debounced change', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(
      <DebounceSearchInput value="" onChange={onChange} aria-label="Search" debounceMs={300} />,
    );

    await user.type(screen.getByRole('textbox', { name: 'Search' }), 'pending');

    const clearButton = screen.getByRole('button', { name: 'Reset' });
    await user.click(clearButton);

    expect(screen.getByRole('textbox', { name: 'Search' })).toHaveValue('');
    expect(onChange).toHaveBeenCalledWith('');

    await vi.advanceTimersByTimeAsync(300);

    await waitFor(() => {
      expect(onChange).toHaveBeenCalledTimes(1);
    });
  });
});
