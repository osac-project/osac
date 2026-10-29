import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type ComputeInstance,
  ComputeInstanceConditionType,
  ComputeInstanceState,
  ConditionStatus,
} from '@osac/types';

import RestartRequiredBanner from './RestartRequiredBanner';
import { renderWithProviders } from '../../test-utils/TestProviders';

const vm = (restart: boolean, state = ComputeInstanceState.RUNNING): ComputeInstance =>
  ({
    id: 'vm-1',
    status: {
      state,
      conditions: restart
        ? [
            {
              type: ComputeInstanceConditionType.RESTART_REQUIRED,
              status: ConditionStatus.TRUE,
            },
          ]
        : [],
    },
  }) as ComputeInstance;

const expandBanner = (user: { click: (el: Element) => Promise<void> }) =>
  user.click(screen.getByRole('button', { name: 'Warning alert details' }));

describe('RestartRequiredBanner', () => {
  it('shows the restart required banner and detected changes', async () => {
    const { user } = renderWithProviders(<RestartRequiredBanner vm={vm(true)} />);

    expect(screen.getByText('Restart required')).toBeInTheDocument();
    await expandBanner(user);
    expect(screen.getByText('Detected changes')).toBeInTheDocument();
    expect(screen.getByText('Details > Instance type')).toBeInTheDocument();
  });

  it('renders nothing when restart is not required', () => {
    renderWithProviders(<RestartRequiredBanner vm={vm(false)} />);

    expect(screen.queryByText('Restart required')).not.toBeInTheDocument();
  });

  it('notes a paused resize in the banner body', async () => {
    const { user } = renderWithProviders(
      <RestartRequiredBanner vm={vm(true, ComputeInstanceState.PAUSED)} />,
    );

    await expandBanner(user);
    expect(screen.getByText(/resized while paused/)).toBeInTheDocument();
  });
});
