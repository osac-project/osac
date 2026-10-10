import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@osac/ui-components/components/Volume/VolumeWizardPage', () => ({
  default: () => <h1>Volume wizard</h1>,
}));

vi.mock('./VolumesListPage', () => ({
  VolumesListPage: () => <h1>Volumes list</h1>,
}));

vi.mock('./VolumeDetailsPage', () => ({
  VolumeDetailsPage: () => <h1>Volume details</h1>,
}));

import { VolumeRoutes } from './VolumeRoutes';

const renderRoutes = (initialEntry: string) => (
  <MemoryRouter initialEntries={[initialEntry]}>
    <Routes>
      <Route path="/storage/volumes/*" element={<VolumeRoutes />} />
    </Routes>
  </MemoryRouter>
);

describe('VolumeRoutes', () => {
  it('renders VolumesListPage on the index route', () => {
    render(renderRoutes('/storage/volumes'));

    expect(screen.getByRole('heading', { name: 'Volumes list' })).toBeInTheDocument();
  });

  it('renders VolumeWizardPage on the create route', () => {
    render(renderRoutes('/storage/volumes/create'));

    expect(screen.getByRole('heading', { name: 'Volume wizard' })).toBeInTheDocument();
  });

  it('renders VolumeDetailsPage on the detail route', () => {
    render(renderRoutes('/storage/volumes/vol-123'));

    expect(screen.getByRole('heading', { name: 'Volume details' })).toBeInTheDocument();
  });

  it('does not expose an edit route', () => {
    render(renderRoutes('/storage/volumes/vol-123/edit'));

    expect(screen.queryByRole('heading', { name: 'Volume wizard' })).not.toBeInTheDocument();
  });
});
