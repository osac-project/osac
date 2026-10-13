import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceConditionType,
  BareMetalInstanceState,
  ConditionStatus,
} from '@osac/types';
import type { BareMetalInstance } from '@osac/types';

import BareMetalDetails from './BareMetalDetails';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('./BareMetalActionButtons', () => ({ default: () => null }));
vi.mock('./BareMetalNetworkingCard', () => ({ default: () => null }));

const instance = {
  id: 'bmi-1',
  metadata: { name: 'bmi-1' },
  status: {
    state: BareMetalInstanceState.PROVISIONING,
    conditions: [
      {
        type: BareMetalInstanceConditionType.PROVISIONED,
        status: ConditionStatus.FALSE,
        reason: 'Provisioning',
        message: 'Provisioning the requested operating system',
      },
    ],
  },
} as unknown as BareMetalInstance;

const readyInstance = {
  ...instance,
  status: {
    ...instance.status,
    conditions: [
      {
        type: BareMetalInstanceConditionType.PROVISIONED,
        status: ConditionStatus.TRUE,
        reason: 'Provisioned',
      },
      {
        type: BareMetalInstanceConditionType.READY,
        status: ConditionStatus.TRUE,
        reason: 'Ready',
      },
    ],
  },
} as unknown as BareMetalInstance;

describe('BareMetalDetails', () => {
  it('places provisioning progress between the resource header and detail cards', () => {
    renderWithProviders(<BareMetalDetails instance={instance} />);

    const progressHeading = screen.getByRole('heading', { name: 'Boot log' });
    const detailsTitle = screen.getByText('Details', { exact: true });

    expect(progressHeading).toBeInTheDocument();
    expect(detailsTitle).toBeInTheDocument();
    expect(
      progressHeading.compareDocumentPosition(detailsTitle) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByText('Conditions', { exact: true })).toBeInTheDocument();
  });

  it('hides provisioning progress after the instance is ready', () => {
    renderWithProviders(<BareMetalDetails instance={readyInstance} />);

    expect(screen.queryByRole('heading', { name: 'Boot log' })).not.toBeInTheDocument();
  });
});
