import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ExternalIPState } from '@osac/types';

import ExternalIpStatusLabel from './ExternalIpStatusLabel';

describe('ExternalIpStatusLabel', () => {
  it.each([
    [ExternalIPState.EXTERNAL_IP_STATE_PENDING, false, 'Provisioning'],
    [ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, false, 'Allocated'],
    [ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, true, 'In use'],
    [ExternalIPState.EXTERNAL_IP_STATE_FAILED, false, 'Failed'],
    [ExternalIPState.EXTERNAL_IP_STATE_DELETING, false, 'Deleting'],
    [ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED, false, 'Unknown'],
  ])('renders the label text for state %s attached=%s', (state, attached, expected) => {
    render(<ExternalIpStatusLabel state={state} attached={attached} />);
    expect(screen.getByText(expected)).toBeInTheDocument();
  });

  it('falls back to the unspecified label when state is undefined', () => {
    render(<ExternalIpStatusLabel />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});
