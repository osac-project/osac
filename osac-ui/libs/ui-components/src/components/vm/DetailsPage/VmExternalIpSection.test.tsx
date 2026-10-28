import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  ComputeInstance,
  ExternalIPAttachment,
  ExternalIPAttachmentsListResponse,
} from '@osac/types';
import { ComputeInstanceState, ExternalIPAttachmentState } from '@osac/types';

import VmExternalIpSection from './VmExternalIpSection';
import * as resourceModule from '../../../api/use-resource';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

const vm = {
  id: 'vm-1',
  metadata: { project: 'tenant-a' },
  status: {
    state: ComputeInstanceState.RUNNING,
    externalIpAddress: '203.0.113.1',
  },
} as ComputeInstance;

const attachment = (state: ExternalIPAttachmentState, message?: string) =>
  ({ status: { state, message } }) as ExternalIPAttachment;

const setAttachment = (items: ExternalIPAttachment[]) => {
  vi.mocked(resourceModule.useListResource).mockReturnValue({
    data: { items } as ExternalIPAttachmentsListResponse,
    isLoading: false,
    isFetching: false,
    error: null,
  } as unknown as ReturnType<typeof resourceModule.useListResource>);
};

describe('VmExternalIpSection', () => {
  it('renders the Host value and failed attachment recovery actions', () => {
    setAttachment([
      attachment(
        ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
        'external IP binding failed',
      ),
    ]);

    renderWithProviders(<VmExternalIpSection vm={vm} />);

    expect(screen.getByRole('heading', { name: 'External IP' })).toBeInTheDocument();
    expect(screen.getByText('Host')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.1')).toBeInTheDocument();
    expect(screen.getByText('External IP attachment failed')).toBeInTheDocument();
    expect(screen.getByText('external IP binding failed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Choose another' })).toBeEnabled();
  });

  it('renders Attach when the running VM has no external IP', () => {
    setAttachment([]);

    renderWithProviders(
      <VmExternalIpSection
        vm={
          {
            ...vm,
            status: { state: ComputeInstanceState.RUNNING, externalIpAddress: '' },
          } as unknown as ComputeInstance
        }
      />,
    );

    expect(screen.getByText('Not set')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Attach external IP' })).toBeEnabled();
  });

  it('shows pending lifecycle feedback while an attachment is provisioning', () => {
    setAttachment([attachment(ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING)]);

    renderWithProviders(
      <VmExternalIpSection
        vm={
          {
            ...vm,
            status: { state: ComputeInstanceState.RUNNING, externalIpAddress: '' },
          } as unknown as ComputeInstance
        }
      />,
    );

    expect(screen.getByRole('button', { name: /Attaching external IP/ })).toBeInTheDocument();
  });
});
