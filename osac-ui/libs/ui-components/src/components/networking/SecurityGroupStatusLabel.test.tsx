import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SecurityGroupState } from '@osac/types';

import { SecurityGroupStatusLabel } from './SecurityGroupStatusLabel';

describe('SecurityGroupStatusLabel', () => {
  it('maps PENDING to progressing/Provisioning', () => {
    render(<SecurityGroupStatusLabel state={SecurityGroupState.PENDING} />);
    expect(screen.getByText('Provisioning')).toBeInTheDocument();
  });

  it('maps READY to ready/Ready', () => {
    render(<SecurityGroupStatusLabel state={SecurityGroupState.READY} />);
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });

  it('maps FAILED to failed/Failed', () => {
    render(<SecurityGroupStatusLabel state={SecurityGroupState.FAILED} />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });

  it('maps DELETING to progressing/Deleting', () => {
    render(<SecurityGroupStatusLabel state={SecurityGroupState.DELETING} />);
    expect(screen.getByText('Deleting')).toBeInTheDocument();
  });

  it('maps undefined to unspecified/Unknown', () => {
    render(<SecurityGroupStatusLabel />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it('maps UNSPECIFIED to unspecified/Unknown', () => {
    render(<SecurityGroupStatusLabel state={SecurityGroupState.UNSPECIFIED} />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});
