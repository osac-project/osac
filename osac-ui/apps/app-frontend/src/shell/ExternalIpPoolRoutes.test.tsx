import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@osac/ui-components/components/ExternalIpPool/ExternalIpPoolsListPage', () => ({
  ExternalIpPoolsListPage: () => <h1>External IP pools</h1>,
}));

vi.mock('@osac/ui-components/components/ExternalIpPool/ExternalIpPoolWizardPage', () => ({
  ExternalIpPoolWizardPage: () => <h1>External IP pool form</h1>,
}));

vi.mock('@osac/ui-components/components/ExternalIpPool/ExternalIpPoolDetailsPage', () => ({
  ExternalIpPoolDetailsPage: () => <h1>External IP pool details</h1>,
}));

import { ExternalIpPoolRoutes } from './ExternalIpPoolRoutes';

const renderRoutes = (initialEntry: string) => (
  <MemoryRouter initialEntries={[initialEntry]}>
    <Routes>
      <Route path="/admin/infrastructure/external-ip-pools/*" element={<ExternalIpPoolRoutes />} />
    </Routes>
  </MemoryRouter>
);

describe('ExternalIpPoolRoutes', () => {
  it('renders the list page on the index route', () => {
    render(renderRoutes('/admin/infrastructure/external-ip-pools'));
    expect(screen.getByRole('heading', { name: 'External IP pools' })).toBeInTheDocument();
  });

  it('renders the form on the create route', () => {
    render(renderRoutes('/admin/infrastructure/external-ip-pools/create'));
    expect(screen.getByRole('heading', { name: 'External IP pool form' })).toBeInTheDocument();
  });

  it('renders the details page on the id route', () => {
    render(renderRoutes('/admin/infrastructure/external-ip-pools/p-1'));
    expect(screen.getByRole('heading', { name: 'External IP pool details' })).toBeInTheDocument();
  });
});
