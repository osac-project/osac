import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { createEmptyComputeInstanceValues } from './payload';
import { VmReviewStep } from './VmReviewStep';
import { renderWithProviders } from '../../../../../test-utils/TestProviders';

const renderReviewStep = (
  specOverrides: Partial<ReturnType<typeof createEmptyComputeInstanceValues>['spec']>,
) => {
  const emptyValues = createEmptyComputeInstanceValues();
  return renderWithProviders(
    <Formik
      initialValues={{ ...emptyValues, spec: { ...emptyValues.spec, ...specOverrides } }}
      onSubmit={() => undefined}
    >
      <VmReviewStep catalogItem={null} />
    </Formik>,
  );
};

describe('VmReviewStep — Storage section', () => {
  it('lists the boot disk and each additional disk with size and resolved tier', async () => {
    renderReviewStep({
      bootDisk: { sizeGib: '40', storageTier: { id: 'id-balanced', name: 'balanced' } },
      additionalDisks: [{ sizeGib: '100', storageTier: { id: 'id-fast', name: 'fast' } }],
    });

    expect(await screen.findByText('Storage')).toBeInTheDocument();
    expect(screen.getByText('40 GB, balanced')).toBeInTheDocument();
    expect(screen.getByText('100 GB, fast')).toBeInTheDocument();
  });

  it('falls back to the raw tier value when no tier matches', async () => {
    renderReviewStep({
      bootDisk: { sizeGib: '40', storageTier: { id: '', name: 'unknown-tier' } },
    });

    expect(await screen.findByText('40 GB, unknown-tier')).toBeInTheDocument();
  });

  it('no longer lists the boot disk under the Configuration section', async () => {
    renderReviewStep({
      bootDisk: { sizeGib: '40', storageTier: { id: 'id-balanced', name: 'balanced' } },
    });

    await screen.findByText('Storage');
    expect(screen.getByText('Configuration')).toBeInTheDocument();
    // "Boot disk" label appears exactly once — inside the Storage section, not Configuration.
    expect(screen.getAllByText('Boot disk')).toHaveLength(1);
  });
});

describe('VmReviewStep — user data source', () => {
  it('shows the selected Secret reference instead of inline user data', async () => {
    renderReviewStep({
      userDataSource: 'secret',
      userData: 'stale inline data',
      userDataSecret: { name: 'user-data-secret' },
    });

    expect(await screen.findByText('user-data-secret')).toBeInTheDocument();
    expect(screen.queryByText('stale inline data')).not.toBeInTheDocument();
  });
});
