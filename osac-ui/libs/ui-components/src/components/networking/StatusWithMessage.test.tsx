import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import StatusWithMessage from './StatusWithMessage';
import { renderWithProviders } from '../../test-utils/TestProviders';
import { ResourceStatusLabel } from '../Resource/ResourceStatusLabel';

describe('StatusWithMessage', () => {
  it('renders the label unchanged without a message', () => {
    renderWithProviders(
      <StatusWithMessage>
        <ResourceStatusLabel status="ready" text="Ready" />
      </StatusWithMessage>,
    );

    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('opens the API message in a Status details popover', async () => {
    const { user } = renderWithProviders(
      <StatusWithMessage message="Updating the security group">
        <ResourceStatusLabel status="ready" text="Ready" />
      </StatusWithMessage>,
    );

    await user.click(screen.getByRole('button', { name: 'Ready' }));
    expect(await screen.findByText('Updating the security group')).toBeInTheDocument();
  });
});
