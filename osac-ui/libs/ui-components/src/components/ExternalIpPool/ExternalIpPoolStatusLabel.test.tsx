import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ExternalIPPoolState } from '@osac/types/private';

import ExternalIpPoolStatusLabel from './ExternalIpPoolStatusLabel';

describe('ExternalIpPoolStatusLabel', () => {
  it.each([
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY, 'Ready'],
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING, 'Provisioning'],
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED, 'Failed'],
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_DELETING, 'Deleting'],
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED, 'Unknown'],
  ])('renders the label text for state %s', (state, expected) => {
    render(<ExternalIpPoolStatusLabel state={state} />);
    expect(screen.getByText(expected)).toBeInTheDocument();
  });

  it('falls back to the unspecified label when state is undefined', () => {
    render(<ExternalIpPoolStatusLabel />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});
