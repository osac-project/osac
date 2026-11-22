import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceSchema, ComputeInstanceState } from '@osac/types';

import VmInstanceCard from './VmInstanceCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('VmInstanceCard', () => {
  describe.each(['tenant-admin', 'tenant-user'] as const)('%s', (role) => {
    it.each([ComputeInstanceState.RUNNING, ComputeInstanceState.DELETING])(
      'keeps details, actions and available metadata visible in state %s',
      (state) => {
        const vm = create(ComputeInstanceSchema, {
          id: 'vm/one',
          metadata: { name: 'web-01' },
          spec: { diskImage: { name: 'rhel-9' } },
          status: { state, externalIpAddress: '203.0.113.1' },
        });

        renderWithProviders(<VmInstanceCard vm={vm} />, { session: { role } });

        expect(screen.getByRole('link', { name: 'web-01' })).toHaveAttribute(
          'href',
          '/vms/vm%2Fone',
        );
        expect(screen.getByRole('button', { name: 'Actions for web-01' })).toBeEnabled();
        expect(screen.getByText('rhel-9')).toBeInTheDocument();
        expect(screen.getByText('203.0.113.1')).toBeInTheDocument();
      },
    );

    it.each([ComputeInstanceState.RUNNING, ComputeInstanceState.DELETING])(
      'uses normal fallbacks for missing disk image and external IP in state %s',
      (state) => {
        const vm = create(ComputeInstanceSchema, {
          id: 'vm-1',
          metadata: { name: 'web-01' },
          status: { state },
        });

        renderWithProviders(<VmInstanceCard vm={vm} />, { session: { role } });

        for (const term of ['Disk image', 'External IP']) {
          expect(screen.getByText(term).closest('dt')?.nextElementSibling).toHaveTextContent('—');
        }
      },
    );

    it('keeps the deleting VM dropdown usable without enabling guarded operations', async () => {
      const vm = create(ComputeInstanceSchema, {
        id: 'vm/one',
        metadata: { name: 'web-01' },
        status: { state: ComputeInstanceState.DELETING },
      });
      const { user } = renderWithProviders(<VmInstanceCard vm={vm} />, {
        session: { role },
      });

      await user.click(screen.getByRole('button', { name: 'Actions for web-01' }));

      const menu = within(screen.getByRole('menu'));
      expect(menu.getByRole('menuitem', { name: 'View details' })).toHaveAttribute(
        'href',
        '/vms/vm%2Fone',
      );
      for (const name of ['Start', 'Stop', 'Restart']) {
        expect(menu.getByRole('menuitem', { name })).toBeDisabled();
      }
      expect(menu.getByRole('menuitem', { name: 'Open console' }).closest('li')).toHaveClass(
        'pf-m-disabled',
      );
      const deleteAction = menu.getByRole('menuitem', { name: 'Delete' });
      expect(deleteAction).toHaveAttribute('aria-disabled', 'true');

      await user.click(deleteAction);

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });
});
