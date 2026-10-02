import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { NetworkAttachmentPickers } from './NetworkAttachmentPickers';
import { type ResourceSelectValue, emptyResourceSelectValue } from './resourceSelectValue';
import { renderWithProviders } from '../../test-utils/TestProviders';

interface TestFormValues {
  useDefaultNetwork: boolean;
  autoExternalIp: boolean;
  net: {
    virtualNetwork: ResourceSelectValue;
    subnet: ResourceSelectValue;
    securityGroups: ResourceSelectValue[];
  };
}

const emptyTestValues = (useDefaultNetwork = true): TestFormValues => ({
  useDefaultNetwork,
  autoExternalIp: false,
  net: {
    virtualNetwork: emptyResourceSelectValue(),
    subnet: emptyResourceSelectValue(),
    securityGroups: [],
  },
});

const renderPickers = (
  props?: Partial<React.ComponentProps<typeof NetworkAttachmentPickers>>,
  initialValues?: TestFormValues,
) => {
  return renderWithProviders(
    <Formik initialValues={initialValues ?? emptyTestValues()} onSubmit={() => undefined}>
      <NetworkAttachmentPickers
        fieldPrefix="net"
        fieldIdPrefix="test"
        useDefaultNetworkName="useDefaultNetwork"
        autoExternalIpName="autoExternalIp"
        {...props}
      />
    </Formik>,
  );
};

describe('NetworkAttachmentPickers', () => {
  it('renders "use default network" and "auto external IP" checkboxes', () => {
    renderPickers();

    expect(
      screen.getByRole('checkbox', { name: 'Use tenant default network' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('checkbox', { name: 'Auto External IP Attachment' }),
    ).toBeInTheDocument();
  });

  it('hides VN, Subnet, and Security Groups pickers when default network is checked', () => {
    renderPickers();

    expect(screen.getByRole('checkbox', { name: 'Use tenant default network' })).toBeChecked();
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet')).not.toBeInTheDocument();
    expect(screen.queryByText('Security groups')).not.toBeInTheDocument();
  });

  it('shows VN, Subnet, and Security Groups pickers when default network is unchecked', async () => {
    const user = userEvent.setup();
    renderPickers();

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();
  });

  it('hides pickers when toggling default network back on', async () => {
    const user = userEvent.setup();
    renderPickers();

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));
    expect(screen.getByText('Virtual network')).toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
  });

  it('renders auto external IP helper text when provided', () => {
    renderPickers({ autoExternalIpHelperText: 'Provision external IPs automatically.' });

    expect(screen.getByText('Provision external IPs automatically.')).toBeInTheDocument();
  });

  it('does not display validation errors on initial render', () => {
    renderPickers(undefined, emptyTestValues(false));

    expect(screen.queryByText('Virtual network is required')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet is required')).not.toBeInTheDocument();
  });
});
