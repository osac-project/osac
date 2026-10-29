/*
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ComputeInstance } from '@osac/types';

import VmUserDataCard from './VmUserDataCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const catalogVm = {
  id: 'vm-1',
  spec: {
    catalogItem: { id: 'catalog-rhel-9' },
    //userData: '#cloud-config\nusers: []',
  },
} as ComputeInstance;


const renderCard = (vm: ComputeInstance = catalogVm) =>
  renderWithProviders(<VmUserDataCard vm={vm} />);


describe('VmUserDataCard', () => {
  it('renders nothing when user data is empty', () => {
    const { container } = renderCard({
      id: 'vm-1',
      spec: { catalogItem: { id: 'catalog-rhel-9' }, userData: '   ' },
    } as ComputeInstance);
    expect(container).toBeEmptyDOMElement();
  });

  it('is collapsed by default and reveals content on expand', async () => {
    const { user } = renderCard();

    expect(screen.getByText('Cloud Init User Data')).toBeInTheDocument();
    expect(screen.queryByText(/#cloud-config/)).not.toBeInTheDocument();

    const toggle = screen.getByRole('button', { name: /Expand user data/ });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);

    expect(screen.getByText(/#cloud-config/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Collapse user data/ })).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });
});
*/
