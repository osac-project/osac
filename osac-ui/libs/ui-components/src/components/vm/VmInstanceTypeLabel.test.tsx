import type { ComponentProps } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { InstanceType } from '@osac/types';

import { VmInstanceTypeLabel } from './VmInstanceTypeLabel';
import { renderWithProviders } from '../../test-utils/TestProviders';

const standardInstanceType = {
  id: 'standard-4-8',
  metadata: { name: 'Standard 4 vCPU / 8 GiB' },
} as InstanceType;

const renderLabel = (props: ComponentProps<typeof VmInstanceTypeLabel>) =>
  renderWithProviders(<VmInstanceTypeLabel {...props} />);

describe('VmInstanceTypeLabel', () => {
  it('shows friendly name when instance type is provided', () => {
    renderLabel({
      instanceTypeId: 'standard-4-8',
      instanceType: standardInstanceType,
    });

    expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();
  });

  it('falls back to raw id when instance type is missing', () => {
    renderLabel({ instanceTypeId: 'standard-4-8' });

    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
  });

  it('shows em dash when instance type id is unset', () => {
    renderLabel({});

    expect(screen.getByText('—')).toBeInTheDocument();
  });

  it('shows skeleton while loading when instance type id is set', () => {
    const { container } = renderLabel({ instanceTypeId: 'standard-4-8', isLoading: true });

    expect(container.querySelector('.pf-v6-c-skeleton')).toBeInTheDocument();
    expect(screen.queryByText('standard-4-8')).not.toBeInTheDocument();
  });

  it('does not show skeleton while loading when instance type id is unset', () => {
    const { container } = renderLabel({ isLoading: true });

    expect(container.querySelector('.pf-v6-c-skeleton')).not.toBeInTheDocument();
    expect(screen.getByText('—')).toBeInTheDocument();
  });
});
