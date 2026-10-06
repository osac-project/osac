import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { VolumeAccessMode } from '@osac/types';

import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('VolumeAccessModeLabel', () => {
  it('maps READ_WRITE_ONCE to ReadWriteOnce', () => {
    renderWithProviders(<VolumeAccessModeLabel accessMode={VolumeAccessMode.READ_WRITE_ONCE} />);
    expect(screen.getByText('ReadWriteOnce')).toBeInTheDocument();
  });

  it('maps READ_ONLY_MANY to ReadOnlyMany', () => {
    renderWithProviders(<VolumeAccessModeLabel accessMode={VolumeAccessMode.READ_ONLY_MANY} />);
    expect(screen.getByText('ReadOnlyMany')).toBeInTheDocument();
  });

  it('maps READ_WRITE_MANY to ReadWriteMany', () => {
    renderWithProviders(<VolumeAccessModeLabel accessMode={VolumeAccessMode.READ_WRITE_MANY} />);
    expect(screen.getByText('ReadWriteMany')).toBeInTheDocument();
  });

  it('maps READ_WRITE_ONCE_POD to ReadWriteOncePod', () => {
    renderWithProviders(
      <VolumeAccessModeLabel accessMode={VolumeAccessMode.READ_WRITE_ONCE_POD} />,
    );
    expect(screen.getByText('ReadWriteOncePod')).toBeInTheDocument();
  });

  it('maps UNSPECIFIED to Unspecified', () => {
    renderWithProviders(<VolumeAccessModeLabel accessMode={VolumeAccessMode.UNSPECIFIED} />);
    expect(screen.getByText('Unspecified')).toBeInTheDocument();
  });

  it('maps undefined to Unspecified', () => {
    renderWithProviders(<VolumeAccessModeLabel />);
    expect(screen.getByText('Unspecified')).toBeInTheDocument();
  });
});
