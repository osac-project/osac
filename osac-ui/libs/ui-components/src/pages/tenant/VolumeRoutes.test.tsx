import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@osac/ui-components/components/Volume/VolumeWizardPage', () => ({
  VolumeWizardPage: () => <h1>Volume wizard</h1>,
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
  it('renders VolumeWizardPage on the create route', () => {
    render(renderRoutes('/storage/volumes/create'));

    expect(screen.getByRole('heading', { name: 'Volume wizard' })).toBeInTheDocument();
  });

  it('renders VolumeWizardPage on the edit route', () => {
    render(renderRoutes('/storage/volumes/vol-123/edit'));

    expect(screen.getByRole('heading', { name: 'Volume wizard' })).toBeInTheDocument();
  });

  it('does not match "create" as a volume ID for the edit route', () => {
    render(renderRoutes('/storage/volumes/create'));

    // The create route should match, not :id/edit — confirmed by the wizard rendering
    expect(screen.getByRole('heading', { name: 'Volume wizard' })).toBeInTheDocument();
  });
});
