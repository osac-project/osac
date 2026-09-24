import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { VolumeWizardPage } from './VolumeWizardPage';

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/storage/volumes/create" element={<VolumeWizardPage />} />
        <Route path="/storage/volumes/:id/edit" element={<VolumeWizardPage />} />
      </Routes>
    </MemoryRouter>,
  );

describe('VolumeWizardPage', () => {
  it('renders in create mode when no id param is present', () => {
    renderAt('/storage/volumes/create');

    expect(screen.getByRole('heading', { name: 'Create volume' })).toBeInTheDocument();
  });

  it('renders in edit mode when an id param is present', () => {
    renderAt('/storage/volumes/vol-abc/edit');

    expect(screen.getByRole('heading', { name: 'Edit volume' })).toBeInTheDocument();
  });
});
