import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

const mocks = vi.hoisted(() => ({
  useOIDCLogin: vi.fn(),
}));

vi.mock('./hooks/oidc-login', () => ({
  useOIDCLogin: mocks.useOIDCLogin,
}));

vi.mock('./shell/AppShell', () => ({
  AppShell: () => <div>App shell</div>,
}));

vi.mock('@osac/ui-components/components/vm/VmConsolePage', () => ({
  default: () => <div>Standalone VM console page</div>,
}));

import App from './App';

describe('App', () => {
  beforeEach(() => {
    mocks.useOIDCLogin.mockReturnValue([
      'test-user',
      'tenant-user',
      'tenant-1',
      false,
      undefined,
      vi.fn().mockResolvedValue(undefined),
    ]);
  });

  it('renders the VM console outside the application shell', () => {
    renderWithProviders(<App />, { routerEntries: ['/vms/vm-1/console'] });

    expect(screen.getByText('Standalone VM console page')).toBeInTheDocument();
    expect(screen.queryByText('App shell')).not.toBeInTheDocument();
  });
});
