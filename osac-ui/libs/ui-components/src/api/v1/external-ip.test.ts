import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentSchema,
  ExternalIPAttachments,
  ExternalIPAttachmentsCreateResponseSchema,
  ExternalIPAttachmentsDeleteResponseSchema,
  ExternalIPs,
} from '@osac/types';

import { useCreateExternalIPAttachment, useDeleteExternalIPAttachment } from './external-ip';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';
import { apiQueryKey } from '../types';

const makeWrapper = (transport: ReturnType<typeof createMockConnectTransport>) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return { queryClient, wrapper };
};

const attachmentInput = create(ExternalIPAttachmentSchema, {
  spec: {
    externalIp: { id: 'ip-1' },
    target: { case: 'cluster', value: { id: 'cluster-1' } },
    targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
  },
});

describe('ExternalIP attachment mutations', () => {
  it('creates an attachment and invalidates attachment and ExternalIP service queries', async () => {
    let capturedObject: unknown;
    const createdAttachment = create(ExternalIPAttachmentSchema, {
      ...attachmentInput,
      id: 'attachment-1',
    });
    const transport = createMockConnectTransport(
      {},
      {
        onExternalIpAttachmentCreate: (request) => {
          capturedObject = request.object;
          return create(ExternalIPAttachmentsCreateResponseSchema, {
            object: createdAttachment,
          });
        },
      },
    );
    const { queryClient, wrapper } = makeWrapper(transport);
    queryClient.setQueryData(apiQueryKey('v1/external_ip_attachments'), { items: [] });
    queryClient.setQueryData([ExternalIPAttachments.typeName], {});
    queryClient.setQueryData([ExternalIPs.typeName], {});

    const { result } = renderHook(() => useCreateExternalIPAttachment(), { wrapper });

    let created: unknown;
    await act(async () => {
      created = await result.current.mutateAsync({ object: attachmentInput });
    });

    expect(capturedObject).toEqual(attachmentInput);
    expect(created).toMatchObject({ object: { id: 'attachment-1' } });
    expect(
      queryClient.getQueryState(apiQueryKey('v1/external_ip_attachments'))?.isInvalidated,
    ).toBe(true);
    expect(queryClient.getQueryState([ExternalIPAttachments.typeName])?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState([ExternalIPs.typeName])?.isInvalidated).toBe(true);
  });

  it('deletes an attachment and invalidates attachment and ExternalIP service queries', async () => {
    let capturedId: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onExternalIpAttachmentDelete: (request) => {
          capturedId = request.id;
          return create(ExternalIPAttachmentsDeleteResponseSchema);
        },
      },
    );
    const { queryClient, wrapper } = makeWrapper(transport);
    queryClient.setQueryData(apiQueryKey('v1/external_ip_attachments'), { items: [] });
    queryClient.setQueryData([ExternalIPAttachments.typeName], {});
    queryClient.setQueryData([ExternalIPs.typeName], {});

    const { result } = renderHook(() => useDeleteExternalIPAttachment(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ id: 'attachment-1' });
    });

    expect(capturedId).toBe('attachment-1');
    expect(
      queryClient.getQueryState(apiQueryKey('v1/external_ip_attachments'))?.isInvalidated,
    ).toBe(true);
    expect(queryClient.getQueryState([ExternalIPAttachments.typeName])?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState([ExternalIPs.typeName])?.isInvalidated).toBe(true);
  });
});
