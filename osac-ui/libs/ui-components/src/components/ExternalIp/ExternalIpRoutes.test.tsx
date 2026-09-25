import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('./ExternalIpListPage', () => ({
  default: () => <h1>External IPs</h1>,
}));

vi.mock('./CreatePage/ExternalIpWizardPage', () => ({
  default: () => <h1>Create external IP</h1>,
}));

import ExternalIpRoutes from './ExternalIpRoutes';

const renderRoutes = (initialEntry: string) => (
  <MemoryRouter initialEntries={[initialEntry]}>
    <Routes>
      <Route path="/networking/external-ips/*" element={<ExternalIpRoutes />} />
    </Routes>
  </MemoryRouter>
);

describe('ExternalIpRoutes', () => {
  it('renders the list page on the index route', () => {
    render(renderRoutes('/networking/external-ips'));

    expect(screen.getByRole('heading', { name: 'External IPs' })).toBeInTheDocument();
  });

  it('renders the wizard on the create route', () => {
    render(renderRoutes('/networking/external-ips/create'));

    expect(screen.getByRole('heading', { name: 'Create external IP' })).toBeInTheDocument();
  });
});
