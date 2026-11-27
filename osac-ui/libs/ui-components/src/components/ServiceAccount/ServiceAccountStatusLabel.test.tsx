import { create } from '@bufbuild/protobuf';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ServiceAccountSchema, ServiceAccountState } from '@osac/types';

import ServiceAccountStatusLabel from './ServiceAccountStatusLabel';

const makeServiceAccount = (state: ServiceAccountState, deleting = false) =>
  create(ServiceAccountSchema, {
    id: 'sa-1',
    metadata: deleting
      ? { deletionTimestamp: { seconds: BigInt(1717000000), nanos: 0 } }
      : undefined,
    status: { state },
  });

describe('ServiceAccountStatusLabel', () => {
  it.each([
    [ServiceAccountState.READY, 'Ready'],
    [ServiceAccountState.FAILED, 'Failed'],
    [ServiceAccountState.PENDING, 'Pending'],
    [ServiceAccountState.UNSPECIFIED, 'Unspecified'],
  ])('renders the label text for state %s', (state, expected) => {
    render(<ServiceAccountStatusLabel serviceAccount={makeServiceAccount(state)} />);

    expect(screen.getByText(expected)).toBeInTheDocument();
  });

  it('falls back to Unspecified when the state is missing', () => {
    render(
      <ServiceAccountStatusLabel serviceAccount={create(ServiceAccountSchema, { id: 'sa-1' })} />,
    );

    expect(screen.getByText('Unspecified')).toBeInTheDocument();
  });

  it('shows Deleting while the service account has a deletion timestamp', () => {
    render(
      <ServiceAccountStatusLabel
        serviceAccount={makeServiceAccount(ServiceAccountState.READY, true)}
      />,
    );

    expect(screen.getByText('Deleting')).toBeInTheDocument();
    expect(screen.queryByText('Ready')).not.toBeInTheDocument();
  });
});
