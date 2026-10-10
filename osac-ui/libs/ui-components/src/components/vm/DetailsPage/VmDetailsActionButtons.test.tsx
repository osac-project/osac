import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance, ExternalIPAttachment } from '@osac/types';
import { ComputeInstanceState, ExternalIPAttachmentState } from '@osac/types';

import VmDetailsActionButtons from './VmDetailsActionButtons';
import { useExternalIPAttachments } from '../../../api/v1/external-ip';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../api/v1/external-ip', () => ({
  useExternalIPAttachments: vi.fn(),
}));

vi.mock('../useVmPowerAction', () => ({
  useVmPowerAction: () => ({ runPowerAction: vi.fn() }),
}));

const vm = {
  id: 'vm-1',
  metadata: { name: 'test-vm' },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;

const attachment = (state: ExternalIPAttachmentState, message?: string) =>
  ({ status: { state, message } }) as ExternalIPAttachment;

const setAttachment = (state: ExternalIPAttachmentState, message?: string) => {
  vi.mocked(useExternalIPAttachments).mockReturnValue({
    data: [attachment(state, message)],
    isLoading: false,
    isFetching: false,
    error: null,
  } as ReturnType<typeof useExternalIPAttachments>);
};

describe('VmDetailsActionButtons external IP states', () => {
  it('shows a disabled progress button while the attachment is provisioning', () => {
    setAttachment(ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING);

    renderWithProviders(<VmDetailsActionButtons vm={vm} />);

    expect(screen.getByRole('button', { name: /Attaching External IP/ })).toBeDisabled();
  });

  it('shows an alert when the attachment fails', () => {
    setAttachment(
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
      'external IP binding failed',
    );

    renderWithProviders(<VmDetailsActionButtons vm={vm} />);

    expect(screen.getByText('External IP attachment failed')).toBeInTheDocument();
    expect(screen.getByText('external IP binding failed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Detach external IP' })).toBeEnabled();
  });
});
