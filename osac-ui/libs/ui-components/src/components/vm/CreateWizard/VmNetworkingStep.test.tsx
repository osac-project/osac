import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { createEmptyComputeInstanceValues } from './fields';
import { VmNetworkingStep } from './VmNetworkingStep';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('VmNetworkingStep', () => {
  it('uses the shared network cards, custom pickers, and external IP switch', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyComputeInstanceValues()} onSubmit={() => undefined}>
        <VmNetworkingStep />
      </Formik>,
    );

    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeChecked();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).not.toBeChecked();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();

    await user.click(screen.getByRole('switch', { name: 'Auto-attach external IP' }));
    expect(
      screen.getByText('Automatically provision an external IP for this virtual machine.'),
    ).toBeInTheDocument();
  });
});
