import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceConditionType,
  BareMetalInstanceSchema,
  BareMetalInstanceState,
  ConditionStatus,
  SecurityGroupSchema,
  SubnetSchema,
  VirtualNetworkSchema,
} from '@osac/types';
import type { BareMetalInstance } from '@osac/types';

import BareMetalDetails from './BareMetalDetails';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('./BareMetalActionButtons', () => ({ default: () => null }));

const instance = {
  id: 'bmi-1',
  metadata: { name: 'bmi-1' },
  status: {
    state: BareMetalInstanceState.PROVISIONING,
    conditions: [
      {
        type: BareMetalInstanceConditionType.PROVISIONED,
        status: ConditionStatus.FALSE,
        reason: 'Provisioning',
        message: 'Provisioning the requested operating system',
      },
    ],
  },
} as unknown as BareMetalInstance;

const readyInstance = {
  ...instance,
  status: {
    ...instance.status,
    conditions: [
      {
        type: BareMetalInstanceConditionType.PROVISIONED,
        status: ConditionStatus.TRUE,
        reason: 'Provisioned',
      },
      {
        type: BareMetalInstanceConditionType.READY,
        status: ConditionStatus.TRUE,
        reason: 'Ready',
      },
    ],
  },
} as unknown as BareMetalInstance;

describe('BareMetalDetails', () => {
  it('places provisioning progress between the resource header and detail cards', () => {
    renderWithProviders(<BareMetalDetails instance={instance} />);

    const progressHeading = screen.getByRole('heading', { name: 'Boot log' });
    const detailsTitle = screen.getByText('Details', { exact: true });

    expect(progressHeading).toBeInTheDocument();
    expect(detailsTitle).toBeInTheDocument();
    expect(
      progressHeading.compareDocumentPosition(detailsTitle) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByText('Conditions', { exact: true })).toBeInTheDocument();
  });

  it('hides provisioning progress after the instance is ready', () => {
    renderWithProviders(<BareMetalDetails instance={readyInstance} />);

    expect(screen.queryByRole('heading', { name: 'Boot log' })).not.toBeInTheDocument();
  });

  it('shows loaded attachment references and status addresses without editing controls', async () => {
    const attachedInstance = create(BareMetalInstanceSchema, {
      id: 'bmi-net',
      metadata: { name: 'bmi-net' },
      spec: {
        networkAttachments: [
          {
            subnet: { id: 'subnet-1' },
            securityGroups: [{ id: 'sg-web' }],
            interface: 'eno1',
            primary: true,
          },
        ],
      },
      status: {
        state: BareMetalInstanceState.RUNNING,
        networkAttachmentStatuses: [
          { interface: 'eno1', subnetRef: 'subnet-1', ipAddress: '192.0.2.10', primary: true },
        ],
        conditions: [
          {
            type: BareMetalInstanceConditionType.READY,
            status: ConditionStatus.TRUE,
            reason: 'Ready',
          },
        ],
      },
    });

    renderWithProviders(<BareMetalDetails instance={attachedInstance} />, {
      apiFixtures: {
        virtualNetworks: [
          create(VirtualNetworkSchema, { id: 'vn-1', metadata: { name: 'prod-vn' } }),
        ],
        subnets: [
          create(SubnetSchema, {
            id: 'subnet-1',
            metadata: { name: 'prod-subnet' },
            spec: { virtualNetwork: { id: 'vn-1' } },
          }),
        ],
        securityGroups: [
          create(SecurityGroupSchema, { id: 'sg-web', metadata: { name: 'web-sg' } }),
        ],
      },
    });

    expect(await screen.findByText('prod-vn')).toBeInTheDocument();
    expect(screen.getByText('prod-subnet')).toBeInTheDocument();
    expect(screen.getByText('web-sg')).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '192.0.2.10' })).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });
});
