import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { BareMetalInstance, BareMetalInstanceCondition } from '@osac/types';
import { BareMetalInstanceConditionType, ConditionStatus } from '@osac/types';

import BareMetalProvisioningProgress from './BareMetalProvisioningProgress';
import { renderWithProviders } from '../../test-utils/TestProviders';

const condition = (
  type: BareMetalInstanceConditionType,
  status: ConditionStatus,
  reason: string,
  message?: string,
): BareMetalInstanceCondition =>
  ({
    type,
    status,
    reason,
    ...(message ? { message } : {}),
  }) as unknown as BareMetalInstanceCondition;

const instanceWithConditions = (conditions: BareMetalInstanceCondition[]): BareMetalInstance =>
  ({
    id: 'bmi-1',
    status: { conditions },
  }) as unknown as BareMetalInstance;

describe('BareMetalProvisioningProgress', () => {
  it('renders four ordered steps with the active phase and curated message', () => {
    renderWithProviders(
      <BareMetalProvisioningProgress
        instance={instanceWithConditions([
          condition(
            BareMetalInstanceConditionType.PROVISIONED,
            ConditionStatus.FALSE,
            'Provisioning',
            'Provisioning the requested operating system',
          ),
        ])}
      />,
    );

    expect(screen.getByRole('heading', { name: 'Boot log' })).toBeInTheDocument();

    const stepper = screen.getByRole('list', { name: 'Provisioning progress steps' });
    const steps = within(stepper).getAllByRole('listitem');

    expect(steps).toHaveLength(4);
    expect(steps.map((step) => step.textContent)).toEqual([
      expect.stringContaining('Host allocation'),
      expect.stringContaining('Provisioning'),
      expect.stringContaining('Network setup'),
      expect.stringContaining('Finalizing'),
    ]);
    expect(screen.getByRole('listitem', { current: 'step' })).toHaveTextContent('Provisioning');
    expect(screen.getByRole('listitem', { name: /^Provisioning, In progress,/ })).toHaveTextContent(
      'Provisioning the requested operating system',
    );
    expect(screen.getByRole('listitem', { name: /^Host allocation, Complete$/ })).toHaveAttribute(
      'id',
      'host',
    );
    expect(document.getElementById('host-title')).toHaveTextContent('Host allocation');
    expect(screen.getAllByText('Complete')).toHaveLength(1);
    expect(screen.queryByText('Not started')).not.toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it.each([
    ['HostAllocation', 'Host allocation'],
    ['Provisioning', 'Provisioning'],
    ['NetworkSetup', 'Network setup'],
  ])('renders %s as the current phase', (reason, title) => {
    renderWithProviders(
      <BareMetalProvisioningProgress
        instance={instanceWithConditions([
          condition(
            BareMetalInstanceConditionType.PROVISIONED,
            ConditionStatus.FALSE,
            reason,
            `${reason} message`,
          ),
        ])}
      />,
    );

    expect(screen.getByRole('listitem', { current: 'step' })).toHaveTextContent(title);
    expect(screen.getByRole('listitem', { current: 'step' })).toHaveTextContent(
      `${reason} message`,
    );
  });

  it.each([
    ['NoMatchingHosts', 'Host allocation'],
    ['HostAllocationFailed', 'Host allocation'],
    ['ProvisionJobFailed', 'Provisioning'],
    ['NetworkAttachmentFailed', 'Network setup'],
    ['NetworkHandoffFailed', 'Network setup'],
    ['IPDiscoveryFailed', 'Network setup'],
  ])('renders %s as a failure on the mapped phase', (reason, title) => {
    const message = `${reason} message`;

    renderWithProviders(
      <BareMetalProvisioningProgress
        instance={instanceWithConditions([
          condition(
            BareMetalInstanceConditionType.PROVISIONED,
            ConditionStatus.FALSE,
            reason,
            message,
          ),
        ])}
      />,
    );

    expect(screen.getByRole('listitem', { current: 'step' })).toHaveTextContent(title);
    expect(screen.getByRole('listitem', { current: 'step' })).toHaveTextContent(message);
    expect(screen.getByText(message)).toBeInTheDocument();
  });

  it('shows ReadyTimeout as a failed Finalizing phase', () => {
    const instance = instanceWithConditions([
      condition(
        BareMetalInstanceConditionType.READY,
        ConditionStatus.FALSE,
        'ReadyTimeout',
        'The host did not reach the requested power state in time',
      ),
      condition(BareMetalInstanceConditionType.PROVISIONED, ConditionStatus.TRUE, 'Provisioned'),
    ]);

    const view = renderWithProviders(<BareMetalProvisioningProgress instance={instance} />);

    expect(screen.getByRole('listitem', { name: /^Finalizing, Failed,/ })).toHaveTextContent(
      'The host did not reach the requested power state in time',
    );
    expect(screen.getAllByText('Complete')).toHaveLength(3);

    view.unmount();
    renderWithProviders(<BareMetalProvisioningProgress instance={instance} />);

    expect(screen.getByRole('listitem', { name: /^Finalizing, Failed,/ })).toHaveTextContent(
      'The host did not reach the requested power state in time',
    );
  });

  it('does not render raw internal error text', () => {
    const internalError = 'Metal3 provider stack trace: secret-token';

    renderWithProviders(
      <BareMetalProvisioningProgress
        instance={instanceWithConditions([
          condition(
            BareMetalInstanceConditionType.PROVISIONED,
            ConditionStatus.FALSE,
            'NetworkAttachmentFailed',
            'Network attachment could not be configured',
          ),
        ])}
      />,
    );

    expect(screen.getByText('Network attachment could not be configured')).toBeInTheDocument();
    expect(screen.queryByText(internalError)).not.toBeInTheDocument();
  });
});
