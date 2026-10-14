import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { StorageBackendState } from '@osac/types/private';

import StorageBackendStatusLabel from './StorageBackendStatusLabel';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('StorageBackendStatusLabel', () => {
  it('maps READY to ready/Ready', () => {
    renderWithProviders(<StorageBackendStatusLabel state={StorageBackendState.READY} />);
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });

  it('maps UNSPECIFIED to progressing/Pending (not ready)', () => {
    renderWithProviders(<StorageBackendStatusLabel state={StorageBackendState.UNSPECIFIED} />);
    expect(screen.getByText('Pending')).toBeInTheDocument();
    expect(screen.queryByText('Ready')).not.toBeInTheDocument();
  });

  it('maps undefined to progressing/Pending (not ready)', () => {
    renderWithProviders(<StorageBackendStatusLabel />);
    expect(screen.getByText('Pending')).toBeInTheDocument();
    expect(screen.queryByText('Ready')).not.toBeInTheDocument();
  });

  it('maps unknown future enum values to Pending (not ready)', () => {
    // Simulate a future enum value the UI does not yet know about
    const futureState = 99 as StorageBackendState;
    renderWithProviders(<StorageBackendStatusLabel state={futureState} />);
    expect(screen.getByText('Pending')).toBeInTheDocument();
    expect(screen.queryByText('Ready')).not.toBeInTheDocument();
  });
});
