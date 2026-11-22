import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceSchema, ComputeInstanceState } from '@osac/types';

import VmInstanceCard from './VmInstanceCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('VmInstanceCard', () => {
  describe.each(['tenant-admin', 'tenant-user'] as const)('%s', (role) => {
    it.each([ComputeInstanceState.RUNNING, ComputeInstanceState.DELETING])(
      'keeps details accessible in state %s while preserving list action guards',
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
        if (state === ComputeInstanceState.DELETING) {
          expect(
            screen.queryByRole('button', { name: 'Actions for web-01' }),
          ).not.toBeInTheDocument();
          expect(screen.queryByText('rhel-9')).not.toBeInTheDocument();
          expect(screen.queryByText('203.0.113.1')).not.toBeInTheDocument();
        } else {
          expect(screen.getByRole('button', { name: 'Actions for web-01' })).toBeInTheDocument();
          expect(screen.getByText('rhel-9')).toBeInTheDocument();
          expect(screen.getByText('203.0.113.1')).toBeInTheDocument();
        }
      },
    );
  });
});
