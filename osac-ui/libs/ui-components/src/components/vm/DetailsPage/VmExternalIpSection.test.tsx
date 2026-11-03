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

const setAttachment = (
  items: ExternalIPAttachment[],
  queryState: { isLoading?: boolean; isFetching?: boolean } = {},
) => {
  vi.mocked(resourceModule.useListResource).mockReturnValue({
    data: { items } as ExternalIPAttachmentsListResponse,
    isLoading: queryState.isLoading ?? false,
    isFetching: queryState.isFetching ?? false,
    error: null,
  } as unknown as ReturnType<typeof resourceModule.useListResource>);
};

describe('VmExternalIpSection', () => {
  it('renders the External IP value and failed attachment recovery actions', () => {
    setAttachment([
      attachment(
        ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
        'external IP binding failed',
      ),
    ]);

    renderWithProviders(<VmExternalIpSection vm={vm} />);

    expect(screen.getByText('External IP')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.1')).toBeInTheDocument();
    expect(screen.getByText('External IP attachment failed')).toBeInTheDocument();
    expect(screen.getByText('external IP binding failed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Choose another' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Detach' })).not.toBeInTheDocument();
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

    expect(screen.getByText('-')).toBeInTheDocument();
    const attachButton = screen.getByRole('button', { name: 'Attach external IP' });
    expect(attachButton).toBeEnabled();
    expect(attachButton).toHaveClass('pf-m-secondary');
  });

  it('keeps Attach visible during a background attachment refresh', () => {
    setAttachment([], { isFetching: true });

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

    expect(screen.getByRole('button', { name: 'Attach external IP' })).toBeInTheDocument();
    expect(screen.queryByText('Attaching external IP...')).not.toBeInTheDocument();
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

    expect(screen.queryByRole('button', { name: 'Attach external IP' })).not.toBeInTheDocument();
    expect(screen.getByText('Attaching external IP...')).toBeInTheDocument();
  });

  it('renders a secondary Detach action for an attached external IP', () => {
    setAttachment([attachment(ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY)]);

    renderWithProviders(<VmExternalIpSection vm={vm} />);

    const detachButton = screen.getByRole('button', { name: 'Detach' });
    expect(detachButton).toHaveClass('pf-m-secondary');
  });
});
