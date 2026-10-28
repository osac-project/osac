import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceCatalogItemSchema,
  SecurityGroupSchema,
  SecurityGroupState,
  SubnetSchema,
  SubnetState,
  VirtualNetworkSchema,
  VirtualNetworkState,
} from '@osac/types';

import { BareMetalNetworkingStep } from './BareMetalNetworkingStep';
import { createEmptyBareMetalInstanceValues, getBareMetalInstanceInitialValues } from './fields';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('BareMetalNetworkingStep', () => {
  it('uses the shared network cards and external IP switch by default', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyBareMetalInstanceValues()} onSubmit={() => undefined}>
        <BareMetalNetworkingStep />
      </Formik>,
    );

    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /Custom network/ })).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).not.toBeChecked();
  });

  it('shows catalog network attachments when the wizard opens', async () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        networkAttachments: {
          behavior: {
            case: 'locked',
            value: {
              items: [
                {
                  subnet: { id: 'subnet-1', name: 'subnet-a' },
                  securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
                },
              ],
            },
          },
        },
      },
    });

    renderWithProviders(
      <Formik
        initialValues={getBareMetalInstanceInitialValues(catalogItem)}
        onSubmit={() => undefined}
      >
        <BareMetalNetworkingStep />
      </Formik>,
      {
        apiFixtures: {
          virtualNetworks: [
            create(VirtualNetworkSchema, {
              id: 'vn-1',
              metadata: { name: 'vn-prod' },
              spec: { ipv4Cidr: '10.0.0.0/16' },
              status: { state: VirtualNetworkState.READY },
            }),
            create(VirtualNetworkSchema, {
              id: 'vn-2',
              metadata: { name: 'vn-dev' },
              spec: { ipv4Cidr: '10.1.0.0/16' },
              status: { state: VirtualNetworkState.READY },
            }),
          ],
          subnets: [
            create(SubnetSchema, {
              id: 'subnet-1',
              metadata: { name: 'subnet-a' },
              spec: { virtualNetwork: { id: 'vn-1', name: 'vn-prod' }, ipv4Cidr: '10.0.1.0/24' },
              status: { state: SubnetState.READY },
            }),
          ],
          securityGroups: [
            create(SecurityGroupSchema, {
              id: 'sg-1',
              metadata: { name: 'sg-web' },
              spec: { virtualNetwork: { id: 'vn-1' }, ingress: [], egress: [] },
              status: { state: SecurityGroupState.READY },
            }),
          ],
        },
      },
    );

    expect(screen.getByRole('radio', { name: /Custom network/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeDisabled();
    await waitFor(() =>
      expect(screen.getByLabelText(/^Virtual network/)).toHaveTextContent('vn-prod'),
    );
    await waitFor(() => expect(screen.getByLabelText(/^Subnet/)).toHaveTextContent('subnet-a'));
    expect(screen.getByLabelText(/^Virtual network/)).toBeDisabled();
    expect(screen.getByLabelText(/^Subnet/)).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).not.toBeChecked();
  });
});
