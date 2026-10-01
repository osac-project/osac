import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Volume, VolumeAccessMode, VolumeState } from '@osac/types';

import VolumeDetailsCard from './VolumeDetailsCard';
import { renderWithProviders } from '../../test-utils/TestProviders';

const fullVolume: Volume = {
  $typeName: 'osac.public.v1.Volume',
  id: 'vol-abc-123',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'my-data-volume',
    tenant: 'acme-corp',
    project: 'ml-training',
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
    storageTier: 'fast-nvme',
    sizeGib: 100n,
    accessMode: VolumeAccessMode.READ_WRITE_ONCE,
  },
  status: {
    $typeName: 'osac.public.v1.VolumeStatus',
    state: VolumeState.AVAILABLE,
    message: 'Volume is ready for use and attached to node worker-3',
  },
} as Volume;

const renderCard = (volume: Volume = fullVolume) =>
  renderWithProviders(<VolumeDetailsCard volume={volume} />);

describe('VolumeDetailsCard', () => {
  it('renders the Identification section with copyable ID and name', () => {
    renderCard();

    expect(screen.getByText('Identification')).toBeInTheDocument();
    expect(screen.getByDisplayValue('vol-abc-123')).toBeInTheDocument();
    expect(screen.getByText('my-data-volume')).toBeInTheDocument();
  });

  it('renders the Configuration section with storage tier, size, and access mode', () => {
    renderCard();

    expect(screen.getByText('Configuration')).toBeInTheDocument();
    expect(screen.getByText('fast-nvme')).toBeInTheDocument();
    expect(screen.getByText('100 GiB')).toBeInTheDocument();
    expect(screen.getByText('ReadWriteOnce')).toBeInTheDocument();
  });

  it('renders the Status section with state badge and message', () => {
    renderCard();

    expect(screen.getByText('Status')).toBeInTheDocument();
    expect(screen.getByText('Available')).toBeInTheDocument();
    expect(screen.getByText('Message')).toBeInTheDocument();
    expect(
      screen.getAllByText('Volume is ready for use and attached to node worker-3').length,
    ).toBeGreaterThanOrEqual(1);
  });

  it('hides the message row when status.message is absent', () => {
    const volumeNoMessage: Volume = {
      ...fullVolume,
      status: {
        $typeName: 'osac.public.v1.VolumeStatus',
        state: VolumeState.CREATING,
      },
    } as Volume;

    renderCard(volumeNoMessage);

    expect(screen.getByText('Creating')).toBeInTheDocument();
    expect(screen.queryByText('Message')).not.toBeInTheDocument();
  });

  it('renders the Metadata section with tenant, project, and created timestamp', () => {
    renderCard();

    expect(screen.getByText('Metadata')).toBeInTheDocument();
    expect(screen.getByText('acme-corp')).toBeInTheDocument();
    expect(screen.getByText('ml-training')).toBeInTheDocument();
    expect(screen.getByText('Created')).toBeInTheDocument();
  });

  it('renders dash fallbacks when optional fields are missing', () => {
    const minimalVolume: Volume = {
      $typeName: 'osac.public.v1.Volume',
      id: 'vol-minimal',
    } as Volume;

    renderCard(minimalVolume);

    expect(screen.getByDisplayValue('vol-minimal')).toBeInTheDocument();
    const dashes = screen.getAllByText('—');
    expect(dashes.length).toBeGreaterThanOrEqual(4);
  });
});
