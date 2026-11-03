import { Route, Routes } from 'react-router-dom';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/components/vm/CreateWizard/VmCreatePage', () => ({
  default: () => <div>VM create route</div>,
}));

vi.mock('@osac/ui-components/components/vm/VmConsolePage', () => ({
  default: () => <div>VM console route</div>,
}));

vi.mock('@osac/ui-components/components/vm/VmDetailsPage', () => ({
  VmDetailsPage: () => <div>VM details route</div>,
}));

vi.mock('./VmListPage', () => ({
  VmListPage: () => <div>VM list route</div>,
}));

const { VmRoutes } = await import('./VmRoutes');

describe('VmRoutes', () => {
  it('registers the dedicated VM console route', () => {
    renderWithProviders(
      <Routes>
        <Route path="/vms/*" element={<VmRoutes />} />
      </Routes>,
      { routerEntries: ['/vms/vm-1/console'] },
    );

    expect(screen.getByText('VM console route')).toBeInTheDocument();
  });
});
