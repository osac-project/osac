import { Button } from '@patternfly/react-core';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ProvisioningFieldGroup from './ProvisioningFieldGroup';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ProvisioningFieldGroup', () => {
  it('renders the group title and children', () => {
    renderWithProviders(
      <ProvisioningFieldGroup title="Disk 1" titleId="disk-1">
        <p>Size control</p>
      </ProvisioningFieldGroup>,
    );

    expect(screen.getByText('Disk 1')).toBeInTheDocument();
    expect(screen.getByText('Size control')).toBeInTheDocument();
  });

  it('renders optional group actions', () => {
    renderWithProviders(
      <ProvisioningFieldGroup
        title="Node set 2"
        titleId="node-set-2"
        actions={<Button variant="plain">Remove node set</Button>}
      >
        <p>Host type</p>
      </ProvisioningFieldGroup>,
    );

    expect(screen.getByRole('button', { name: 'Remove node set' })).toBeInTheDocument();
  });
});
