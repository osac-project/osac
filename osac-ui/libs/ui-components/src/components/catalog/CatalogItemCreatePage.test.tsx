import { Route, Routes } from 'react-router-dom';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { CatalogItemCreatePage } from './CatalogItemCreatePage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('../../hooks/use-session', () => ({
  useSession: () => ({ role: 'tenant-admin' }),
}));

const renderCreatePage = (routerEntries: string[]) =>
  renderWithProviders(
    <Routes>
      <Route path="/catalog/:kind/create" element={<CatalogItemCreatePage />} />
    </Routes>,
    { routerEntries },
  );

describe('CatalogItemCreatePage', () => {
  it.each(['bm', 'cluster'])('renders a placeholder for the %s catalog item kind', (kind) => {
    renderCreatePage([`/catalog/${kind}/create`]);

    expect(
      screen.getByRole('heading', { name: 'Catalog item authoring is coming soon' }),
    ).toBeInTheDocument();
  });

  it('renders the compute catalog item wizard for the vm kind', () => {
    renderCreatePage(['/catalog/vm/create']);

    expect(screen.getByRole('heading', { name: 'Create catalog item' })).toBeInTheDocument();
  });

  it('shows a not-found page for an unrecognized catalog item kind', () => {
    renderCreatePage(['/catalog/not-a-kind/create']);

    expect(screen.getByRole('heading', { name: 'Catalog item not found' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Return to catalog' })).toBeInTheDocument();
  });
});
