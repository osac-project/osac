import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import BareMetalPowerStateFilter from './BareMetalPowerStateFilter';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('BareMetalPowerStateFilter', () => {
  it('calls onChange with the selected power state', async () => {
    const onChange = vi.fn();
    const { user } = renderWithProviders(
      <BareMetalPowerStateFilter selected={undefined} onChange={onChange} />,
    );

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by power state' }));
    await user.click(screen.getByRole('option', { name: 'Running' }));

    expect(onChange).toHaveBeenCalledWith('running');
  });

  it('clears the filter when All power states is selected', async () => {
    const onChange = vi.fn();
    const { user } = renderWithProviders(
      <BareMetalPowerStateFilter selected="running" onChange={onChange} />,
    );

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by power state' }));
    await user.click(screen.getByRole('option', { name: 'All power states' }));

    expect(onChange).toHaveBeenCalledWith(undefined);
  });
});
