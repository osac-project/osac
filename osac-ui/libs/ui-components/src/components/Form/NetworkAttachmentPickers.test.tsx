import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { NetworkAttachmentPickers } from './NetworkAttachmentPickers';
import { type ResourceSelectValue, emptyResourceSelectValue } from './resourceSelectValue';
import { renderWithProviders } from '../../test-utils/TestProviders';

interface TestFormValues {
  net: {
    virtualNetwork: ResourceSelectValue;
    subnet: ResourceSelectValue;
    securityGroups: ResourceSelectValue[];
  };
}

const emptyTestValues = (): TestFormValues => ({
  net: {
    virtualNetwork: emptyResourceSelectValue(),
    subnet: emptyResourceSelectValue(),
    securityGroups: [],
  },
});

const renderPickers = (props?: Partial<React.ComponentProps<typeof NetworkAttachmentPickers>>) => {
  return renderWithProviders(
    <Formik initialValues={emptyTestValues()} onSubmit={() => undefined}>
      <NetworkAttachmentPickers fieldPrefix="net" fieldIdPrefix="test" {...props} />
    </Formik>,
  );
};

describe('NetworkAttachmentPickers', () => {
  it('renders VN, Subnet, and Security Groups picker labels', () => {
    renderPickers();

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();
  });

  it('does not display validation errors on initial render', () => {
    renderPickers();

    expect(screen.queryByText('Virtual network is required')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet is required')).not.toBeInTheDocument();
  });
});
