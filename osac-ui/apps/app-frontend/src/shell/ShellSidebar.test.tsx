import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { UserRole } from '@osac/ui-components/shellTypes';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

import { ShellSidebar } from './ShellSidebar';

const renderSidebar = (entry: string, role: UserRole) =>
  renderWithProviders(<ShellSidebar />, { routerEntries: [entry], session: { role } });

const networkingLinks = () => {
  const toggle = screen.getByRole('button', { name: 'Networking', hidden: true });
  const section = toggle.closest('li') as HTMLElement;
  return within(section)
    .getAllByRole('link', { hidden: true })
    .map((link) => link.textContent);
};

describe('ShellSidebar networking', () => {
  it('lists the provider admin networking pages, pools and Network class last', () => {
    renderSidebar('/networking/subnets', 'admin');

    expect(networkingLinks()).toEqual([
      'Virtual networks',
      'Subnets',
      'Security groups',
      'NAT gateways',
      'External IPs',
      'External IP pools',
      'Network class',
    ]);
  });

  it.each<UserRole>(['tenant-admin', 'tenant-user'])(
    'lists the tenant networking pages for a %s',
    (role) => {
      renderSidebar('/networking/subnets', role);

      expect(networkingLinks()).toEqual([
        'Virtual networks',
        'Subnets',
        'Security groups',
        'NAT gateways',
        'External IPs',
      ]);
    },
  );

  it.each([
    ['/networking/security-groups/sg-1/edit-rules', 'Security groups'],
    ['/networking/virtual-networks/vn-1', 'Virtual networks'],
    ['/networking/nat-gateways/create/vn-1', 'NAT gateways'],
    ['/networking/virtual-networks/vn-1/nat-gateway/attach', 'Virtual networks'],
    ['/networking/external-ips/create', 'External IPs'],
    ['/admin/infrastructure/external-ip-pools/pool-1', 'External IP pools'],
  ])('keeps the nav item active on %s', (entry, label) => {
    renderSidebar(entry, 'admin');

    expect(screen.getByRole('link', { name: label, hidden: true })).toHaveAttribute(
      'aria-current',
      'page',
    );
  });
});
