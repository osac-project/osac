import { create } from '@bufbuild/protobuf';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ClusterSchema,
  ClusterState,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentState,
} from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import ClusterExternalIpCard, { groupAttachmentsByEndpoint } from './ClusterExternalIpCard';
import * as externalIpModule from '../../../api/v1/external-ip';

vi.mock('../../../api/v1/external-ip', () => ({
  useExternalIPAttachments: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

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
  message?: string,
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
      message,
    },
  }) as unknown as ExternalIPAttachment;

const makeCluster = () =>
  create(ClusterSchema, {
    id: 'cl-1',
    status: {
      state: ClusterState.READY,
      apiEndpoint: 'api.example.com',
      ingressEndpoint: 'apps.example.com',
    },
  });

describe('groupAttachmentsByEndpoint', () => {
  it('groups ready and lifecycle attachments by endpoint', () => {
    const apiAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
    );
    const ingressAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
      'Attaching external IP',
    );

    const result = groupAttachmentsByEndpoint([apiAttachment, ingressAttachment]);

    expect(result.api).toBe(apiAttachment);
    expect(result.ingress).toBe(ingressAttachment);
  });

  it('keeps an endpoint occupied while its attachment is deleting', () => {
    const deletingAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
    );

    const result = groupAttachmentsByEndpoint([deletingAttachment]);

    expect(result.api).toBe(deletingAttachment);
  });
});

describe('ClusterExternalIpCard', () => {
  it('renders both endpoint URLs and independent attach actions without empty-state text', () => {
    const onAttach = vi.fn();
    const onDetach = vi.fn();
    mockUseExternalIPAttachments();

    const { container } = render(
      <ClusterExternalIpCard cluster={makeCluster()} onAttach={onAttach} onDetach={onDetach} />,
    );

    expect(screen.getByText('External IP endpoints')).toBeInTheDocument();
    expect(container.querySelector('.pf-v6-c-card.pf-m-secondary')).toBeInTheDocument();
    expect(screen.getByText('api.example.com')).toBeInTheDocument();
    expect(screen.getByText('apps.example.com')).toBeInTheDocument();
    expect(screen.queryByText('Not attached')).not.toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Attach External IP' })).toHaveLength(2);
  });

  it('shows the attached address and detach action only for the occupied endpoint', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
    );
    const onAttach = vi.fn();
    const onDetach = vi.fn();
    mockUseExternalIPAttachments([attachment]);

    render(
      <ClusterExternalIpCard cluster={makeCluster()} onAttach={onAttach} onDetach={onDetach} />,
    );

    expect(screen.getByText('api.example.com')).toBeInTheDocument();
    expect(screen.getByText('Attached')).toBeInTheDocument();
    expect(screen.queryByText('203.0.113.10')).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Detach External IP from API endpoint' }),
    ).toBeInTheDocument();
    expect(screen.queryByText('Not attached')).not.toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Attach External IP' })).toHaveLength(1);
  });

  it('disables detach while an attachment is still being provisioned', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
    );
    mockUseExternalIPAttachments([attachment]);

    render(<ClusterExternalIpCard cluster={makeCluster()} onAttach={vi.fn()} onDetach={vi.fn()} />);

    expect(
      screen.getByRole('button', { name: 'Detach External IP from API endpoint' }),
    ).toBeDisabled();
  });

  it('shows auto attachment progress and disables attach while auto-provisioned endpoints are pending', () => {
    const autoCluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: { autoExternalIpAttachment: true },
      status: {
        state: ClusterState.READY,
        apiEndpoint: 'api.example.com',
        ingressEndpoint: 'apps.example.com',
      },
    });
    mockUseExternalIPAttachments();

    render(<ClusterExternalIpCard cluster={autoCluster} onAttach={vi.fn()} onDetach={vi.fn()} />);

    expect(screen.getAllByText('Auto attaching in progress')).toHaveLength(2);
    expect(screen.queryByText('Auto-provisioned')).not.toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Attach External IP' })).toHaveLength(2);
    screen
      .getAllByRole('button', { name: 'Attach External IP' })
      .forEach((button) => expect(button).toBeDisabled());
  });

  it('uses only the auto status label while an auto attachment lifecycle is active', () => {
    const pendingApiAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
    );
    const deletingIngressAttachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
    );
    const autoCluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: { autoExternalIpAttachment: true },
      status: {
        state: ClusterState.READY,
        apiEndpoint: 'api.example.com',
        ingressEndpoint: 'apps.example.com',
      },
    });
    mockUseExternalIPAttachments([pendingApiAttachment, deletingIngressAttachment]);

    render(<ClusterExternalIpCard cluster={autoCluster} onAttach={vi.fn()} onDetach={vi.fn()} />);

    expect(screen.getAllByText('Auto attaching in progress')).toHaveLength(2);
    expect(screen.queryByText('Attaching')).not.toBeInTheDocument();
    expect(screen.queryByText('Detaching')).not.toBeInTheDocument();
  });

  it('shows auto attached after an auto-provisioned endpoint attachment is ready', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
    );
    const autoCluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: { autoExternalIpAttachment: true },
      status: {
        state: ClusterState.READY,
        apiEndpoint: 'api.example.com',
        ingressEndpoint: 'apps.example.com',
      },
    });
    mockUseExternalIPAttachments([attachment]);

    render(<ClusterExternalIpCard cluster={autoCluster} onAttach={vi.fn()} onDetach={vi.fn()} />);

    expect(screen.getByText('Auto attached')).toBeInTheDocument();
    expect(screen.getByText('Auto attaching in progress')).toBeInTheDocument();
  });

  it('sends the endpoint-specific action to the details tab', async () => {
    const onAttach = vi.fn();
    const onDetach = vi.fn();
    const user = userEvent.setup();
    mockUseExternalIPAttachments();

    const { rerender } = render(
      <ClusterExternalIpCard cluster={makeCluster()} onAttach={onAttach} onDetach={onDetach} />,
    );

    await user.click(screen.getAllByRole('button', { name: 'Attach External IP' })[0]);
    expect(onAttach).toHaveBeenCalledWith(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
    );

    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      '203.0.113.10',
    );
    mockUseExternalIPAttachments([attachment]);
    rerender(
      <ClusterExternalIpCard cluster={makeCluster()} onAttach={onAttach} onDetach={onDetach} />,
    );

    await user.click(screen.getByRole('button', { name: 'Detach External IP from API endpoint' }));
    expect(onDetach).toHaveBeenCalledWith(attachment);
  });

  it('keeps lifecycle attachments occupied and displays their status message', () => {
    const attachment = makeAttachment(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      '',
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
      'IP was already consumed',
    );
    mockUseExternalIPAttachments([attachment]);

    render(<ClusterExternalIpCard cluster={makeCluster()} onAttach={vi.fn()} onDetach={vi.fn()} />);

    expect(screen.getByText('IP was already consumed')).toBeInTheDocument();
    expect(screen.queryByText('Not attached')).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Detach External IP from Ingress endpoint' }),
    ).toBeInTheDocument();
  });

  it('loads attachments once with the cluster filter', () => {
    mockUseExternalIPAttachments();

    render(<ClusterExternalIpCard cluster={makeCluster()} onAttach={vi.fn()} onDetach={vi.fn()} />);

    expect(externalIpModule.useExternalIPAttachments).toHaveBeenCalledTimes(1);
    expect(externalIpModule.useExternalIPAttachments).toHaveBeenCalledWith(
      { filter: 'this.spec.cluster.id == "cl-1"' },
      { enabled: true },
    );
  });

  it('uses the NAT gateway card loading and error states', () => {
    mockUseExternalIPAttachments([], { isLoading: true });
    const { rerender } = render(
      <ClusterExternalIpCard cluster={makeCluster()} onAttach={vi.fn()} onDetach={vi.fn()} />,
    );
    expect(screen.getByLabelText('Loading external IP attachments')).toBeInTheDocument();

    mockUseExternalIPAttachments([], { isLoading: false, error: new Error('Network error') });
    rerender(
      <ClusterExternalIpCard cluster={makeCluster()} onAttach={vi.fn()} onDetach={vi.fn()} />,
    );
    expect(screen.getByText('Failed to load external IP attachments')).toBeInTheDocument();
  });
});
