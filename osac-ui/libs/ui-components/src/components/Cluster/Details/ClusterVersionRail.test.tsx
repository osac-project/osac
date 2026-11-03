import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import ClusterVersionRail from './ClusterVersionRail';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterVersionRail', () => {
  it('shows the observed current version and opens the upgrade dialog from the rail', async () => {
    const onUpgrade = vi.fn();
    const { user } = renderWithProviders(
      <ClusterVersionRail
        currentVersion="4.17.0"
        action={{ kind: 'available' }}
        onUpgrade={onUpgrade}
      />,
    );

    expect(screen.getByText('Cluster version')).toBeInTheDocument();
    expect(screen.getByText('Current version')).toBeInTheDocument();
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    expect(screen.getByText('Upgrade available')).toBeInTheDocument();
    expect(screen.queryByText('Desired version')).not.toBeInTheDocument();

    const action = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(action).toBeEnabled();
    await user.click(action);
    expect(onUpgrade).toHaveBeenCalledOnce();
  });

  it.each([
    'The observed control-plane version is unavailable.',
    'No eligible target version is available.',
    'The cluster is not ready for an upgrade.',
    'Could not load cluster versions.',
  ])('shows a visible, associated reason when blocked: %s', (reason) => {
    const onUpgrade = vi.fn();
    renderWithProviders(
      <ClusterVersionRail action={{ kind: 'blocked', reason }} onUpgrade={onUpgrade} />,
    );

    expect(screen.getByText('—')).toBeInTheDocument();
    expect(screen.getByText('No upgrade available')).toBeInTheDocument();
    const action = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(action).toBeDisabled();
    expect(action).toHaveAccessibleDescription(reason);
    expect(screen.getByText(reason)).toBeVisible();
    expect(onUpgrade).not.toHaveBeenCalled();
  });

  it('keeps the action disabled while upgrade options load', () => {
    renderWithProviders(<ClusterVersionRail action={{ kind: 'loading' }} onUpgrade={vi.fn()} />);

    const action = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(action).toBeDisabled();
    expect(action).toHaveAccessibleDescription('Checking upgrade options');
    expect(screen.getByText('Checking upgrade options')).toBeVisible();
  });
});
