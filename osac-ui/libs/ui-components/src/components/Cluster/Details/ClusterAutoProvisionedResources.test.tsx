import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ClusterSchema,
  type ExternalIP,
  type ExternalIPAttachment,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentSchema,
  ExternalIPAttachmentState,
  ExternalIPSchema,
  ExternalIPState,
} from '@osac/types';

import ClusterAutoProvisionedResources from './ClusterAutoProvisionedResources';
import { ClusterOverviewTab } from './ClusterOverviewTab';
import { ApiProvider } from '../../../api/api-context';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';

const CLUSTER_ID = 'cl-auto-1';

const makeExternalIp = (
  overrides: Partial<{ id: string; name: string; state: ExternalIPState; address: string }> = {},
): ExternalIP =>
  create(ExternalIPSchema, {
    id: overrides.id ?? 'eip-1',
    metadata: {
      name: overrides.name ?? 'auto-eip-api',
      labels: { 'auto-created-for': CLUSTER_ID },
    },
    status: {
      state: overrides.state ?? ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
      address: overrides.address ?? '198.51.100.1',
    },
  });

const makeAttachment = (
  overrides: Partial<{
    id: string;
    name: string;
    state: ExternalIPAttachmentState;
    endpoint: ExternalIPAttachmentEndpoint;
    address: string;
  }> = {},
): ExternalIPAttachment =>
  create(ExternalIPAttachmentSchema, {
    id: overrides.id ?? 'eipa-1',
    metadata: {
      name: overrides.name ?? 'auto-eipa-api',
      labels: { 'auto-created-for': CLUSTER_ID },
    },
    spec: {
      targetEndpoint:
        overrides.endpoint ?? ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
    },
    status: {
      state: overrides.state ?? ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
      externalIpAddress: overrides.address ?? '198.51.100.1',
    },
  });

const renderWithProviders = (
  ui: React.ReactElement,
  fixtures: {
    externalIps?: ExternalIP[];
    externalIpAttachments?: ExternalIPAttachment[];
  } = {},
) => {
  const transport = createMockConnectTransport(fixtures);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return render(ui, { wrapper });
};

describe('ClusterAutoProvisionedResources', () => {
  it('is not rendered when autoExternalIpAttachment is false', () => {
    const cluster = create(ClusterSchema, {
      id: CLUSTER_ID,
      spec: { autoExternalIpAttachment: false },
    });

    renderWithProviders(<ClusterOverviewTab cluster={cluster} />);

    expect(screen.queryByText('Auto-provisioned resources')).not.toBeInTheDocument();
  });

  it('is not rendered when autoExternalIpAttachment is undefined', () => {
    const cluster = create(ClusterSchema, {
      id: CLUSTER_ID,
    });

    renderWithProviders(<ClusterOverviewTab cluster={cluster} />);

    expect(screen.queryByText('Auto-provisioned resources')).not.toBeInTheDocument();
  });

  it('shows auto-provisioned ExternalIPs and ExternalIPAttachments', async () => {
    const eip = makeExternalIp();
    const eipa = makeAttachment();

    renderWithProviders(<ClusterAutoProvisionedResources clusterId={CLUSTER_ID} />, {
      externalIps: [eip],
      externalIpAttachments: [eipa],
    });

    expect(await screen.findByText('auto-eip-api')).toBeInTheDocument();
    expect(screen.getByText('auto-eipa-api')).toBeInTheDocument();
    expect(screen.getByText('ExternalIP')).toBeInTheDocument();
    expect(screen.getByText('ExternalIPAttachment')).toBeInTheDocument();
  });

  it('shows correct status labels for resources', async () => {
    const eip = makeExternalIp({
      state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    });
    const eipa = makeAttachment({
      state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
    });

    renderWithProviders(<ClusterAutoProvisionedResources clusterId={CLUSTER_ID} />, {
      externalIps: [eip],
      externalIpAttachments: [eipa],
    });

    expect(await screen.findByText('Allocated')).toBeInTheDocument();
    expect(screen.getByText('Pending')).toBeInTheDocument();
  });

  it('shows endpoint labels for attachments', async () => {
    const eipaApi = makeAttachment({
      id: 'eipa-api',
      name: 'att-api',
      endpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
    });
    const eipaIngress = makeAttachment({
      id: 'eipa-ingress',
      name: 'att-ingress',
      endpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
    });

    renderWithProviders(<ClusterAutoProvisionedResources clusterId={CLUSTER_ID} />, {
      externalIpAttachments: [eipaApi, eipaIngress],
    });

    expect(await screen.findByText('API')).toBeInTheDocument();
    expect(screen.getByText('Ingress')).toBeInTheDocument();
  });

  it('shows empty state message when no auto-provisioned resources exist', async () => {
    renderWithProviders(<ClusterAutoProvisionedResources clusterId={CLUSTER_ID} />, {
      externalIps: [],
      externalIpAttachments: [],
    });

    expect(await screen.findByText('No auto-provisioned resources.')).toBeInTheDocument();
  });
});
