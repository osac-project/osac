import { create } from '@bufbuild/protobuf';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  ClusterSchema,
  ClusterState,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentState,
} from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import ClusterNetworkingCard, { groupAttachmentsByEndpoint } from './ClusterNetworkingCard';
import * as externalIpModule from '../../../api/v1/external-ip';

vi.mock('../../../api/v1/external-ip', () => ({
  useExternalIPAttachments: vi.fn(),
}));

const mockUseExternalIPAttachments = (
  attachments: ExternalIPAttachment[] = [],
  overrides: Record<string, unknown> = {},
) => {
  vi.mocked(externalIpModule.useExternalIPAttachments).mockReturnValue({
    data: attachments,
    isLoading: false,
    isFetching: false,
    error: null,
    ...overrides,
  } as unknown as ReturnType<typeof externalIpModule.useExternalIPAttachments>);
};

const makeAttachment = (
  endpoint: ExternalIPAttachmentEndpoint,
  ipAddress: string,
  state: ExternalIPAttachmentState = ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
): ExternalIPAttachment =>
  ({
    id: `eipa-${endpoint}`,
    spec: {
      targetEndpoint: endpoint,
      target: {
        case: 'cluster',
        value: { id: 'cl-1', name: 'my-cluster' },
      },
    },
    status: {
      state,
      externalIpAddress: ipAddress,
    },
  }) as unknown as ExternalIPAttachment;

describe('groupAttachmentsByEndpoint', () => {
  it('returns empty attachment status when no attachments exist', () => {
    const result = groupAttachmentsByEndpoint([]);

    expect(result.api.attachment).toBeUndefined();
    expect(result.api.externalIpAddress).toBeUndefined();
    expect(result.ingress.attachment).toBeUndefined();
    expect(result.ingress.externalIpAddress).toBeUndefined();
  });

  it('groups API endpoint attachment correctly', () => {
    const apiAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
    );
    const result = groupAttachmentsByEndpoint([apiAttachment]);

    expect(result.api.attachment).toBe(apiAttachment);
    expect(result.api.externalIpAddress).toBe('203.0.113.10');
    expect(result.ingress.attachment).toBeUndefined();
    expect(result.ingress.externalIpAddress).toBeUndefined();
  });

  it('groups Ingress endpoint attachment correctly', () => {
    const ingressAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '203.0.113.20',
    );
    const result = groupAttachmentsByEndpoint([ingressAttachment]);

    expect(result.api.attachment).toBeUndefined();
    expect(result.ingress.attachment).toBe(ingressAttachment);
    expect(result.ingress.externalIpAddress).toBe('203.0.113.20');
  });

  it('groups both API and Ingress attachments', () => {
    const apiAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
    );
    const ingressAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '203.0.113.20',
    );
    const result = groupAttachmentsByEndpoint([apiAttachment, ingressAttachment]);

    expect(result.api.externalIpAddress).toBe('203.0.113.10');
    expect(result.ingress.externalIpAddress).toBe('203.0.113.20');
  });

  it('preserves empty externalIpAddress from the attachment', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
    );
    const result = groupAttachmentsByEndpoint([attachment]);

    expect(result.api.attachment).toBe(attachment);
    expect(result.api.externalIpAddress).toBe('');
  });

  it('ignores attachments with UNSPECIFIED endpoint', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED,
      '1.2.3.4',
    );
    const result = groupAttachmentsByEndpoint([attachment]);

    expect(result.api.attachment).toBeUndefined();
    expect(result.ingress.attachment).toBeUndefined();
  });

  it('excludes PENDING attachments', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
    );
    const result = groupAttachmentsByEndpoint([attachment]);

    expect(result.api.attachment).toBeUndefined();
    expect(result.api.externalIpAddress).toBeUndefined();
  });

  it('excludes FAILED attachments', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '203.0.113.20',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
    );
    const result = groupAttachmentsByEndpoint([attachment]);

    expect(result.ingress.attachment).toBeUndefined();
    expect(result.ingress.externalIpAddress).toBeUndefined();
  });

  it('excludes DELETING attachments', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
    );
    const result = groupAttachmentsByEndpoint([attachment]);

    expect(result.api.attachment).toBeUndefined();
    expect(result.api.externalIpAddress).toBeUndefined();
  });

  it('includes only READY attachment when mixed states exist', () => {
    const pendingAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
    );
    const readyAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '203.0.113.20',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
    );
    const result = groupAttachmentsByEndpoint([pendingAttachment, readyAttachment]);

    expect(result.api.attachment).toBeUndefined();
    expect(result.ingress.attachment).toBe(readyAttachment);
    expect(result.ingress.externalIpAddress).toBe('203.0.113.20');
  });
});

describe('ClusterNetworkingCard', () => {
  it('displays the resolved subnet name', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'my-subnet' },
          securityGroups: [],
        },
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('my-subnet')).toBeInTheDocument();
  });

  it('displays a comma-separated list of security group names', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-2',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'subnet-a' },
          securityGroups: [
            { id: 'sg-1', name: 'sg-alpha' },
            { id: 'sg-2', name: 'sg-beta' },
          ],
        },
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('sg-alpha, sg-beta')).toBeInTheDocument();
  });

  it('shows dash for empty networking fields', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-3',
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const dashes = screen.getAllByText('—');
    expect(dashes.length).toBeGreaterThanOrEqual(4);
  });

  it('displays pod CIDR and service CIDR', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-cidr',
      spec: {
        network: {
          podCidr: '10.128.0.0/14',
          serviceCidr: '172.30.0.0/16',
        },
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('10.128.0.0/14')).toBeInTheDocument();
    expect(screen.getByText('172.30.0.0/16')).toBeInTheDocument();
  });

  it('shows "Awaiting provisioning" for endpoints while cluster is progressing', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-4',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'my-subnet' },
          securityGroups: [],
        },
      },
      status: {
        state: ClusterState.PROGRESSING,
        apiEndpoint: '',
        ingressEndpoint: '',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const awaitingElements = screen.getAllByText('Awaiting provisioning');
    expect(awaitingElements).toHaveLength(2);
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows dash for endpoints when cluster is in FAILED state', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-failed',
      status: {
        state: ClusterState.FAILED,
        apiEndpoint: '',
        ingressEndpoint: '',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const dashes = screen.getAllByText('—');
    expect(dashes.length).toBeGreaterThanOrEqual(2);
  });

  it('shows resolved API and ingress endpoints when ready', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-5',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'my-subnet' },
          securityGroups: [],
        },
      },
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows "Auto-provisioned" label next to endpoints when autoExternalIpAttachment is true', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-auto',
      spec: {
        autoExternalIpAttachment: true,
      },
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const labels = screen.getAllByText('Auto-provisioned');
    expect(labels).toHaveLength(2);
    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
  });

  it('does not show "Auto-provisioned" label when autoExternalIpAttachment is false', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-no-auto',
      spec: {
        autoExternalIpAttachment: false,
      },
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.queryByText('Auto-provisioned')).not.toBeInTheDocument();
    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
  });

  it('does not show "Auto-provisioned" label when autoExternalIpAttachment is undefined', () => {
    mockUseExternalIPAttachments();
    const cluster = create(ClusterSchema, {
      id: 'cl-undef',
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.queryByText('Auto-provisioned')).not.toBeInTheDocument();
  });

  describe('external IP attachment status', () => {
    it('shows external IP label for API endpoint when attached', () => {
      const apiAttachment = makeAttachment(
        ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
        '203.0.113.10',
      );
      mockUseExternalIPAttachments([apiAttachment]);

      const cluster = create(ClusterSchema, {
        id: 'cl-1',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    });

    it('shows external IP label for Ingress endpoint when attached', () => {
      const ingressAttachment = makeAttachment(
        ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
        '203.0.113.20',
      );
      mockUseExternalIPAttachments([ingressAttachment]);

      const cluster = create(ClusterSchema, {
        id: 'cl-1',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.getByText('203.0.113.20')).toBeInTheDocument();
    });

    it('shows external IP labels for both endpoints when both attached', () => {
      const apiAttachment = makeAttachment(
        ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
        '203.0.113.10',
      );
      const ingressAttachment = makeAttachment(
        ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
        '203.0.113.20',
      );
      mockUseExternalIPAttachments([apiAttachment, ingressAttachment]);

      const cluster = create(ClusterSchema, {
        id: 'cl-1',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
      expect(screen.getByText('203.0.113.20')).toBeInTheDocument();
    });

    it('does not show external IP labels when neither endpoint is attached', () => {
      mockUseExternalIPAttachments([]);

      const cluster = create(ClusterSchema, {
        id: 'cl-1',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.queryByText('203.0.113.10')).not.toBeInTheDocument();
      expect(screen.queryByText('203.0.113.20')).not.toBeInTheDocument();
    });

    it('calls useExternalIPAttachments with cluster-scoped filter from EndpointDescription', () => {
      mockUseExternalIPAttachments();

      const cluster = create(ClusterSchema, {
        id: 'cl-filter-test',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      // Each EndpointDescription calls useExternalIPAttachments with the same filter;
      // TanStack Query deduplicates the underlying network request.
      expect(externalIpModule.useExternalIPAttachments).toHaveBeenCalledWith(
        { filter: 'this.spec.cluster.id == "cl-filter-test"' },
        { enabled: true },
      );
    });

    it('shows loading spinner for external IP while attachments are loading', () => {
      mockUseExternalIPAttachments([], { isLoading: true });

      const cluster = create(ClusterSchema, {
        id: 'cl-loading',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      const spinners = screen.getAllByLabelText('Loading external IP');
      expect(spinners).toHaveLength(2);
    });

    it('does not show green label for PENDING attachment', () => {
      const pendingAttachment = makeAttachment(
        ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
        '203.0.113.10',
        ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
      );
      mockUseExternalIPAttachments([pendingAttachment]);

      const cluster = create(ClusterSchema, {
        id: 'cl-1',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.queryByText('203.0.113.10')).not.toBeInTheDocument();
    });

    it('does not show green label for FAILED attachment', () => {
      const failedAttachment = makeAttachment(
        ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
        '203.0.113.20',
        ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
      );
      mockUseExternalIPAttachments([failedAttachment]);

      const cluster = create(ClusterSchema, {
        id: 'cl-1',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.queryByText('203.0.113.20')).not.toBeInTheDocument();
    });

    it('shows error labels when attachment query fails', () => {
      mockUseExternalIPAttachments([], { error: new Error('Network error') });

      const cluster = create(ClusterSchema, {
        id: 'cl-error',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      const errorLabels = screen.getAllByText('External IP error');
      expect(errorLabels).toHaveLength(2);
    });

    it('does not show error labels when query succeeds', () => {
      mockUseExternalIPAttachments([]);

      const cluster = create(ClusterSchema, {
        id: 'cl-ok',
        status: {
          state: ClusterState.READY,
          apiEndpoint: '10.0.0.10',
          ingressEndpoint: '10.0.0.42',
        },
      });

      render(<ClusterNetworkingCard cluster={cluster} />);

      expect(screen.queryByText('External IP error')).not.toBeInTheDocument();
    });
  });
});
