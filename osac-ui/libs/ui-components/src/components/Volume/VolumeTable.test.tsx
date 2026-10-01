import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Volume, VolumeAccessMode, VolumeSchema, VolumeState } from '@osac/types';

import { VolumeTable } from './VolumeTable';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeVolume = (overrides: {
  id: string;
  name?: string;
  project?: string;
  storageTier?: string;
  sizeGib?: bigint;
  accessMode?: VolumeAccessMode;
  state?: VolumeState;
}): Volume =>
  create(VolumeSchema, {
    id: overrides.id,
    metadata: {
      name: overrides.name ?? `vol-${overrides.id}`,
      project: overrides.project ?? 'default-project',
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      storageTier: overrides.storageTier ?? 'standard',
      sizeGib: overrides.sizeGib ?? BigInt(100),
      accessMode: overrides.accessMode ?? VolumeAccessMode.READ_WRITE_ONCE,
    },
    status: {
      state: overrides.state ?? VolumeState.AVAILABLE,
    },
  });

const renderTable = (volumes: Volume[]) =>
  renderWithProviders(
    <Routes>
      <Route path="/storage/volumes" element={<VolumeTable volumes={volumes} />} />
      <Route path="/storage/volumes/:id" element={<h1>Volume detail page</h1>} />
    </Routes>,
    { routerEntries: ['/storage/volumes'] },
  );

describe('VolumeTable', () => {
  it('renders the required column headers in order', () => {
    renderTable([makeVolume({ id: 'v-1' })]);

    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual([
      'Name',
      'State',
      'Project',
      'Storage Tier',
      'Size',
      'Access Mode',
      'Created',
      '', // Actions column (aria-label only)
    ]);
  });

  it('renders the volume name as a link to /storage/volumes/{id}', () => {
    renderTable([makeVolume({ id: 'vol-abc', name: 'my-volume' })]);

    const link = screen.getByRole('link', { name: 'my-volume' });
    expect(link).toHaveAttribute('href', '/storage/volumes/vol-abc');
  });

  it('formats size as {n} GiB', () => {
    renderTable([makeVolume({ id: 'v-1', sizeGib: BigInt(256) })]);

    expect(screen.getByText('256 GiB')).toBeInTheDocument();
  });

  it('renders VolumeStatusLabel for the state column', () => {
    renderTable([makeVolume({ id: 'v-1', state: VolumeState.CREATING })]);

    expect(screen.getByText('Creating')).toBeInTheDocument();
  });

  it('renders VolumeAccessModeLabel for the access mode column', () => {
    renderTable([makeVolume({ id: 'v-1', accessMode: VolumeAccessMode.READ_WRITE_MANY })]);

    expect(screen.getByText('ReadWriteMany')).toBeInTheDocument();
  });

  it('renders project name', () => {
    renderTable([makeVolume({ id: 'v-1', project: 'team-alpha' })]);

    expect(screen.getByText('team-alpha')).toBeInTheDocument();
  });

  it('renders "Default" when project is an empty string', () => {
    renderTable([makeVolume({ id: 'v-1', project: '' })]);

    const projectCell = screen
      .getAllByRole('cell')
      .find((cell) => cell.getAttribute('data-label') === 'Project');
    expect(projectCell).toHaveTextContent('Default');
  });

  it('renders storage tier name', () => {
    renderTable([makeVolume({ id: 'v-1', storageTier: 'premium-ssd' })]);

    expect(screen.getByText('premium-ssd')).toBeInTheDocument();
  });

  it('shows the empty state when no volumes are provided', () => {
    renderTable([]);

    expect(screen.getByText('No volumes found')).toBeInTheDocument();
  });

  it('navigates to the detail route when the name link is clicked', async () => {
    const { user } = renderTable([makeVolume({ id: 'vol-123', name: 'test-vol' })]);

    await user.click(screen.getByRole('link', { name: 'test-vol' }));

    expect(screen.getByRole('heading', { name: 'Volume detail page' })).toBeInTheDocument();
  });

  it('renders the actions menu for an AVAILABLE volume', () => {
    renderTable([makeVolume({ id: 'v-1', name: 'my-vol', state: VolumeState.AVAILABLE })]);

    expect(screen.getByRole('button', { name: 'Actions for my-vol' })).toBeInTheDocument();
  });

  it('does not render the actions menu for a DELETING volume', () => {
    renderTable([makeVolume({ id: 'v-1', name: 'my-vol', state: VolumeState.DELETING })]);

    expect(screen.queryByRole('button', { name: 'Actions for my-vol' })).not.toBeInTheDocument();
  });

  it('shows em-dash for missing fields when spec is absent', () => {
    const vol = create(VolumeSchema, {
      id: 'v-no-spec',
      metadata: { name: 'no-spec-vol' },
    });
    renderTable([vol]);

    const cells = screen.getAllByRole('cell');
    const sizeCell = cells.find((cell) => cell.getAttribute('data-label') === 'Size');
    expect(sizeCell).toHaveTextContent('—');
  });
});
