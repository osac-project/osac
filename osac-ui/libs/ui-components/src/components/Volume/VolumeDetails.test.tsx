import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Volume, VolumeAccessMode, VolumeState } from '@osac/types';

import VolumeDetails from './VolumeDetails';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeVolume = (state: VolumeState): Volume =>
  ({
    $typeName: 'osac.public.v1.Volume',
    id: 'vol-test-1',
    metadata: {
      $typeName: 'osac.public.v1.Metadata',
      name: 'test-volume',
      tenant: 'test-tenant',
      project: 'test-project',
      creator: 'alice',
      creationTimestamp: {
        $typeName: 'google.protobuf.Timestamp',
        seconds: 1767225600n,
        nanos: 0,
      },
      displayName: '',
      description: '',
      annotations: {},
      labels: {},
      version: 1,
    },
    spec: {
      $typeName: 'osac.public.v1.VolumeSpec',
      storageTier: 'balanced',
      sizeGib: 50n,
      accessMode: VolumeAccessMode.READ_WRITE_ONCE,
    },
    status: {
      $typeName: 'osac.public.v1.VolumeStatus',
      state,
    },
  }) as Volume;

const renderDetails = (volume: Volume) => renderWithProviders(<VolumeDetails volume={volume} />);

describe('VolumeDetails', () => {
  it('shows a delete button when volume state is AVAILABLE', () => {
    renderDetails(makeVolume(VolumeState.AVAILABLE));

    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('shows a delete button when volume state is FAILED', () => {
    renderDetails(makeVolume(VolumeState.FAILED));

    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('hides the delete button when volume state is CREATING', () => {
    renderDetails(makeVolume(VolumeState.CREATING));

    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('hides the delete button when volume state is DELETING', () => {
    renderDetails(makeVolume(VolumeState.DELETING));

    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('hides the delete button when volume state is DELETED', () => {
    renderDetails(makeVolume(VolumeState.DELETED));

    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('hides the delete button when volume state is UNSPECIFIED', () => {
    renderDetails(makeVolume(VolumeState.UNSPECIFIED));

    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('renders the header with volume name and breadcrumb', () => {
    renderDetails(makeVolume(VolumeState.AVAILABLE));

    expect(screen.getByRole('heading', { name: 'test-volume' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Volumes' })).toHaveAttribute(
      'href',
      '/storage/volumes',
    );
  });
});
