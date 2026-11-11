import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import StatusWithMessage from './StatusWithMessage';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('StatusWithMessage', () => {
  it('opens the message in a popover from a label with a visible cue', async () => {
    const { user } = renderWithProviders(
      <StatusWithMessage message="Fabric Manager rejected the VNI">
        <span>Failed</span>
      </StatusWithMessage>,
    );

    const trigger = screen.getByRole('button', { name: 'Failed' });
    expect(trigger.querySelector('svg')).toBeInTheDocument();
    await user.click(trigger);

    expect(await screen.findByText('Status details')).toBeInTheDocument();
    expect(screen.getByText('Fabric Manager rejected the VNI')).toBeInTheDocument();
  });

  it('renders the plain label without a message', () => {
    renderWithProviders(
      <StatusWithMessage>
        <span>Ready</span>
      </StatusWithMessage>,
    );

    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
