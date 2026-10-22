import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { createEmptyComputeInstanceValues } from './payload';
import { VmReviewStep } from './VmReviewStep';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const renderReviewStep = (
  specOverrides: Partial<ReturnType<typeof createEmptyComputeInstanceValues>['spec']>,
) => {
  const emptyValues = createEmptyComputeInstanceValues();
  return renderWithProviders(
    <Formik
      initialValues={{ ...emptyValues, spec: { ...emptyValues.spec, ...specOverrides } }}
      onSubmit={() => undefined}
    >
      <VmReviewStep />
    </Formik>,
  );
};

describe('VmReviewStep — Storage section', () => {
  it('lists the boot disk and each additional disk with size and resolved tier', async () => {
    renderReviewStep({
      bootDisk: {
        sizeGib: provisioningFieldValue('40'),
        storageTier: provisioningFieldValue({ id: 'id-balanced', name: 'balanced' }),
      },
      additionalDisks: provisioningFieldValue([
        { sizeGib: '100', storageTier: { id: 'id-fast', name: 'fast' } },
      ]),
    });

    expect(await screen.findByText('Storage')).toBeInTheDocument();
    expect(screen.getByText('40 GB, balanced')).toBeInTheDocument();
    expect(screen.getByText('100 GB, fast')).toBeInTheDocument();
  });

  it('falls back to the raw tier value when no tier matches', async () => {
    renderReviewStep({
      bootDisk: {
        sizeGib: provisioningFieldValue('40'),
        storageTier: provisioningFieldValue({ id: '', name: 'unknown-tier' }),
      },
    });

    expect(await screen.findByText('40 GB, unknown-tier')).toBeInTheDocument();
  });

  it('no longer lists the boot disk under the Configuration section', async () => {
    renderReviewStep({
      bootDisk: {
        sizeGib: provisioningFieldValue('40'),
        storageTier: provisioningFieldValue({ id: 'id-balanced', name: 'balanced' }),
      },
    });

    await screen.findByText('Storage');
    expect(screen.getByText('Configuration')).toBeInTheDocument();
    // "Boot disk" label appears exactly once — inside the Storage section, not Configuration.
    expect(screen.getAllByText('Boot disk')).toHaveLength(1);
  });
});

describe('VmReviewStep — disk image', () => {
  it('shows the selected disk image after the SSH public key', async () => {
    renderReviewStep({
      diskImage: provisioningFieldValue({ id: 'disk-image-rhel', name: 'rhel' }),
    });

    expect(await screen.findByText('Disk image')).toBeInTheDocument();
    expect(screen.getByText('rhel')).toBeInTheDocument();
  });
});

describe('VmReviewStep — user data source', () => {
  it('shows the selected Secret reference instead of inline user data', async () => {
    renderReviewStep({
      userDataSource: provisioningFieldValue('secret'),
      userData: provisioningFieldValue('stale inline data'),
      userDataSecret: provisioningFieldValue('user-data-secret'),
    });

    expect(await screen.findByText('user-data-secret')).toBeInTheDocument();
    expect(screen.queryByText('stale inline data')).not.toBeInTheDocument();
  });
});
