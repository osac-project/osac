import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ExternalIPState } from '@osac/types';

import ExternalIpStatusLabel from './ExternalIpStatusLabel';

describe('ExternalIpStatusLabel', () => {
  it.each([
    [ExternalIPState.EXTERNAL_IP_STATE_PENDING, 'Provisioning'],
    [ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, 'Allocated'],
    [ExternalIPState.EXTERNAL_IP_STATE_FAILED, 'Failed'],
    [ExternalIPState.EXTERNAL_IP_STATE_DELETING, 'Deleting'],
    [ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED, 'Unknown'],
  ])('renders the API state %s as %s', (state, expected) => {
    render(<ExternalIpStatusLabel state={state} />);
    expect(screen.getByText(expected)).toBeInTheDocument();
  });

  it('shows Allocated, not a usage, for an attached external IP', () => {
    render(<ExternalIpStatusLabel state={ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED} />);
    expect(screen.getByText('Allocated')).toBeInTheDocument();
    expect(screen.queryByText('In use')).not.toBeInTheDocument();
  });

  it('falls back to the unspecified label when state is undefined', () => {
    render(<ExternalIpStatusLabel />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});
