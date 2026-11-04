import { createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstances } from '@osac/types';

import VmDeleteConfirmModal from './VmDeleteConfirmModal';
import { useListResource } from '../../../api/use-resource';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const vm = {
  id: 'vm-1',
  metadata: { name: 'test-vm', project: 'test-project' },
} as ComputeInstance;

const ComputeInstanceListObserver = () => {
  useListResource(ComputeInstances);
  return null;
};

describe('VmDeleteConfirmModal', () => {
  it('refreshes the VM list after a successful delete', async () => {
    const list = vi.fn(() => ({ items: [], size: 0, total: 0 }));
    const transport = createRouterTransport((router) => {
      router.service(ComputeInstances, {
        list,
        delete: () => ({}),
      });
    });

    const { user } = renderWithProviders(
      <>
        <ComputeInstanceListObserver />
        <VmDeleteConfirmModal vm={vm} onClose={vi.fn()} onSuccess={vi.fn()} />
      </>,
      { transport },
    );

    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
  });
});
