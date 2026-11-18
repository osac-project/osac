import { create } from '@bufbuild/protobuf';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ClusterSchema, Clusters } from '@osac/types';

import ClusterDeleteConfirmModal from './ClusterDeleteConfirmModal';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockCluster = create(ClusterSchema, {
  id: 'cluster-1',
  metadata: { name: 'my-cluster' },
  spec: { autoExternalIpAttachment: true },
});
const mockClusterWithoutAutoIp = create(ClusterSchema, {
  id: 'cluster-2',
  metadata: { name: 'no-auto-ip-cluster' },
});

const renderModal = (cluster = mockCluster, deleteError?: Error) => {
  const requests: string[] = [];
  const onClose = vi.fn();
  const onSuccess = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(Clusters, {
      delete: (request) => {
        requests.push(request.id);
        if (deleteError) {
          throw deleteError;
        }
        return {};
      },
    });
  });
  const view = renderWithProviders(
    <ClusterDeleteConfirmModal cluster={cluster} onClose={onClose} onSuccess={onSuccess} />,
    { transport },
  );
  return { ...view, requests, onClose, onSuccess };
};

describe('ClusterDeleteConfirmModal', () => {
  it('renders the deletion warning and resource cleanup info alert', () => {
    renderModal();

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
  });

  it('does not show resource cleanup warning when autoExternalIpAttachment is not set', () => {
    renderModal(mockClusterWithoutAutoIp);

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

  it('deletes the cluster by ID and reports success', async () => {
    const { user, requests, onSuccess } = renderModal();

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce());
    expect(requests).toEqual(['cluster-1']);
  });

  it('calls onClose on Cancel click', async () => {
    const { user, onClose, requests } = renderModal();

    await user.click(screen.getByRole('button', { name: /Cancel/i }));

    expect(onClose).toHaveBeenCalledOnce();
    expect(requests).toEqual([]);
  });

  it('shows an inline error when deletion fails', async () => {
    const { user, onSuccess } = renderModal(
      mockCluster,
      new ConnectError('permission denied', Code.PermissionDenied),
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    expect(await screen.findByText('permission denied')).toBeInTheDocument();
    expect(screen.getByText('Failed to delete cluster')).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });
});
