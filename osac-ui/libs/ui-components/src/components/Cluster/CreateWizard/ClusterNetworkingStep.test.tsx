import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { ClusterNetworkingStep } from './ClusterNetworkingStep';
import { createEmptyClusterValues } from './fields';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterNetworkingStep', () => {
  it('renders Infrastructure Networking and Cluster Networking sections', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    expect(screen.getByText('Infrastructure Networking')).toBeInTheDocument();
    expect(screen.getByText('Cluster Networking')).toBeInTheDocument();
  });

  it('hides pickers when default network toggle is ON (default)', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    expect(screen.getByRole('checkbox', { name: 'Use tenant default network' })).toBeChecked();
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet')).not.toBeInTheDocument();
    expect(screen.queryByText('Security groups')).not.toBeInTheDocument();
  });

  it('shows pickers when default network toggle is OFF', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();
  });

  it('hides pickers when default network toggle is toggled back ON', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    // Toggle OFF
    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));
    expect(screen.getByText('Virtual network')).toBeInTheDocument();

    // Toggle back ON
    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
  });

  it('renders Auto External IP toggle independently', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    const autoExternalIpSwitch = screen.getByRole('checkbox', {
      name: 'Auto External IP Attachment',
    });
    expect(autoExternalIpSwitch).not.toBeChecked();
    expect(
      screen.getByText(
        'Automatically provision external IPs for the cluster API and ingress endpoints.',
      ),
    ).toBeInTheDocument();
  });

  it('allows toggling Auto External IP independently of default network toggle', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    const autoExternalIpSwitch = screen.getByRole('checkbox', {
      name: 'Auto External IP Attachment',
    });
    await user.click(autoExternalIpSwitch);

    expect(autoExternalIpSwitch).toBeChecked();
    // Default network toggle remains ON
    expect(screen.getByRole('checkbox', { name: 'Use tenant default network' })).toBeChecked();
  });

  it('renders Pod CIDR and Service CIDR fields in Cluster Networking section', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    expect(screen.getByLabelText(/Pod CIDR/)).toBeInTheDocument();
    expect(screen.getByLabelText(/Service CIDR/)).toBeInTheDocument();
  });

  it('accepts CIDR input values', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    const podCidr = screen.getByLabelText(/Pod CIDR/);
    await user.type(podCidr, '10.128.0.0/14');
    expect(podCidr).toHaveValue('10.128.0.0/14');
  });
});
