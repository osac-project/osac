import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import ClusterDeleteConfirmModal from './ClusterDeleteConfirmModal';
import * as clusterApi from '../../api/v1/cluster';

vi.mock('../../api/v1/cluster', async (importOriginal) => {
  const actual = await importOriginal<typeof clusterApi>();
  return {
    ...actual,
    useDeleteCluster: vi.fn(),
  };
});

const mockCluster = {
  id: 'cluster-1',
  metadata: { name: 'my-cluster' },
  spec: { autoExternalIpAttachment: true },
  status: { state: 1 },
};

const mockClusterWithoutAutoIp = {
  id: 'cluster-2',
  metadata: { name: 'no-auto-ip-cluster' },
  status: { state: 1 },
};

const createMockMutation = (overrides = {}) => ({
  mutate: vi.fn(),
  isPending: false,
  error: null,
  reset: vi.fn(),
  ...overrides,
});

describe('ClusterDeleteConfirmModal', () => {
  const mutate = vi.fn();
  const reset = vi.fn();

  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(clusterApi.useDeleteCluster).mockReturnValue(
      createMockMutation({ mutate, reset }) as unknown as ReturnType<
        typeof clusterApi.useDeleteCluster
      >,
    );
  });

  it('renders the deletion warning and resource cleanup info alert', () => {
    render(
      <ClusterDeleteConfirmModal
        cluster={mockCluster as never}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(
      screen.getByText(
        'This permanently deletes the cluster and all its resources. This action cannot be undone.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('Resource cleanup')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Auto-provisioned External IPs and External IP Attachments will be permanently deleted.',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        'Manually created External IP Attachments will be detached and returned to Pending status.',
      ),
    ).toBeInTheDocument();
  });

  it('does not show resource cleanup warning when autoExternalIpAttachment is not set', () => {
    render(
      <ClusterDeleteConfirmModal
        cluster={mockClusterWithoutAutoIp as never}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(
      screen.getByText(
        'This permanently deletes the cluster and all its resources. This action cannot be undone.',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText('Resource cleanup')).not.toBeInTheDocument();
    expect(
      screen.queryByText(
        'Auto-provisioned External IPs and External IP Attachments will be permanently deleted.',
      ),
    ).not.toBeInTheDocument();
  });

  it('calls delete mutation with cluster ID on Delete click', async () => {
    const user = userEvent.setup();
    mutate.mockImplementation((_id: string, options?: { onSuccess?: () => void }) => {
      options?.onSuccess?.();
      return Promise.resolve(undefined);
    });
    const onSuccess = vi.fn();

    render(
      <ClusterDeleteConfirmModal
        cluster={mockCluster as never}
        onClose={vi.fn()}
        onSuccess={onSuccess}
      />,
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    expect(reset).toHaveBeenCalled();
    expect(mutate).toHaveBeenCalledWith('cluster-1', {
      onSuccess: expect.any(Function) as unknown,
    });
    expect(onSuccess).toHaveBeenCalled();
  });

  it('calls onClose on Cancel click', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    render(
      <ClusterDeleteConfirmModal
        cluster={mockCluster as never}
        onClose={onClose}
        onSuccess={vi.fn()}
      />,
    );

    await user.click(screen.getByRole('button', { name: /Cancel/i }));

    expect(reset).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('shows error alert when mutation has an error', () => {
    vi.mocked(clusterApi.useDeleteCluster).mockReturnValue(
      createMockMutation({
        mutate,
        reset,
        error: new Error('permission denied'),
      }) as unknown as ReturnType<typeof clusterApi.useDeleteCluster>,
    );

    render(
      <ClusterDeleteConfirmModal
        cluster={mockCluster as never}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    expect(screen.getByText('Failed to delete cluster')).toBeInTheDocument();
    expect(screen.getByText('permission denied')).toBeInTheDocument();
  });
});
