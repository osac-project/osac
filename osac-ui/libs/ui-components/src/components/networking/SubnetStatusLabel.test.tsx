import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SubnetState } from '@osac/types';

import { SubnetStatusLabel } from './SubnetStatusLabel';

describe('SubnetStatusLabel', () => {
  it('maps PENDING to progressing/Provisioning', () => {
    render(<SubnetStatusLabel state={SubnetState.PENDING} />);
    expect(screen.getByText('Provisioning')).toBeInTheDocument();
  });

  it('maps READY to ready/Ready', () => {
    render(<SubnetStatusLabel state={SubnetState.READY} />);
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });

  it('maps FAILED to failed/Failed', () => {
    render(<SubnetStatusLabel state={SubnetState.FAILED} />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });

  it('maps DELETING to progressing/Deleting', () => {
    render(<SubnetStatusLabel state={SubnetState.DELETING} />);
    expect(screen.getByText('Deleting')).toBeInTheDocument();
  });

  it('maps undefined to unspecified/Unknown', () => {
    render(<SubnetStatusLabel />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it('maps UNSPECIFIED to unspecified/Unknown', () => {
    render(<SubnetStatusLabel state={SubnetState.UNSPECIFIED} />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});
