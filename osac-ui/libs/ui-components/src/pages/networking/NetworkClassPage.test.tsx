import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type NetworkClass,
  NetworkClassSchema,
  NetworkClassState,
  Protocol,
} from '@osac/types/private';

import { NetworkClassPage } from './NetworkClassPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const networkClass = () =>
  create(NetworkClassSchema, {
    id: 'nc-1',
    title: 'EVPN on CloudVision',
    description: 'Tenant networks on the EVPN fabric.',
    fabricManager: 'cvp',
    k8sManager: 'cudn_evpn',
    spec: {
      defaults: {
        virtualNetworkIpv4Cidr: '10.200.0.0/16',
        subnetIpv4Cidr: '10.200.0.0/20',
        ingressRules: [{ protocol: Protocol.TCP, portFrom: 22, portTo: 22, ipv4Cidr: '0.0.0.0/0' }],
        egressRules: [{ protocol: Protocol.ALL, ipv4Cidr: '0.0.0.0/0' }],
      },
      eastWestCapabilities: { supportsEastWestEthernet: true },
      addressPlan: { overlay: 0.5, fabric: 0.25, lbVip: 0.25 },
    },
    status: {
      state: NetworkClassState.READY,
      message: 'Managers resolved',
      networkManagers: {
        fabric: { name: 'cvp', ansibleRole: 'osac.fabric.cvp' },
        kubernetes: { name: 'cudn_evpn', ansibleRole: 'osac.k8s.cudn_evpn' },
      },
      networkType: 'evpn',
      securityGroupIcmp: false,
    },
  });

const renderPage = (networkClasses: NetworkClass[] = [networkClass()]) =>
  renderWithProviders(<NetworkClassPage />, {
    apiFixtures: { networkClasses },
    session: { role: 'admin' },
  });

describe('NetworkClassPage', () => {
  it('shows the network class, its managers and its status', async () => {
    const { user } = renderPage();

    expect(await screen.findByText('EVPN on CloudVision')).toBeInTheDocument();
    const details = within(screen.getByLabelText('Network class'));
    expect(details.getByText('Tenant networks on the EVPN fabric.')).toBeInTheDocument();
    expect(details.getByText('Ready')).toBeInTheDocument();
    await user.click(details.getByRole('button', { name: 'Ready' }));
    expect(await screen.findByText('Managers resolved')).toBeInTheDocument();
    // Each manager is shown once: the resolved name, with its Ansible role.
    expect(details.getByText('Fabric manager')).toBeInTheDocument();
    expect(details.getByText('Kubernetes manager')).toBeInTheDocument();
    expect(details.getByText('cvp')).toBeInTheDocument();
    expect(details.getByText('osac.fabric.cvp')).toBeInTheDocument();
    expect(details.getByText('cudn_evpn')).toBeInTheDocument();
    expect(details.getByText('osac.k8s.cudn_evpn')).toBeInTheDocument();
    expect(details.queryByText(/Resolved/)).not.toBeInTheDocument();
    expect(details.getByText('evpn')).toBeInTheDocument();
  });

  it('shows the requested managers until they are resolved', async () => {
    const unresolved = networkClass();
    unresolved.k8sManager = '';
    if (unresolved.status) {
      unresolved.status.networkManagers = undefined;
    }
    renderPage([unresolved]);

    const details = within(await screen.findByLabelText('Network class'));
    expect(details.getByText('cvp (not resolved yet)')).toBeInTheDocument();
    expect(details.getByText('None (virtual machines are disabled)')).toBeInTheDocument();
  });

  it('shows the tenant defaults', async () => {
    renderPage();

    const defaults = within(await screen.findByLabelText('Tenant defaults'));
    expect(defaults.getByText('10.200.0.0/16')).toBeInTheDocument();
    expect(defaults.getByText('10.200.0.0/20')).toBeInTheDocument();
    const inbound = within(screen.getByRole('grid', { name: 'Inbound rules' }));
    expect(inbound.getByText('TCP')).toBeInTheDocument();
    expect(inbound.getByText('22')).toBeInTheDocument();
    const outbound = within(screen.getByRole('grid', { name: 'Outbound rules' }));
    expect(outbound.getByText('All')).toBeInTheDocument();
    // The defaults are read-only, so the empty text does not point to Edit rules.
    expect(screen.queryByText(/Use Edit rules/)).not.toBeInTheDocument();
  });

  it('says when a default rule direction is empty', async () => {
    const noEgress = networkClass();
    if (noEgress.spec?.defaults) {
      noEgress.spec.defaults.egressRules = [];
    }
    renderPage([noEgress]);

    expect(await screen.findByText('No outbound rules.')).toBeInTheDocument();
  });

  it('shows the subnet address plan as percentages with their use', async () => {
    const plan = networkClass();
    if (plan.spec?.addressPlan) {
      plan.spec.addressPlan.overlay = 0.5625;
      plan.spec.addressPlan.fabric = 0.1875;
    }
    renderPage([plan]);

    const addressPlan = within(await screen.findByLabelText('Subnet address plan'));
    expect(addressPlan.getByText('56.25%')).toBeInTheDocument();
    expect(addressPlan.getByText('18.75%')).toBeInTheDocument();
    expect(addressPlan.getByText('25%')).toBeInTheDocument();
    expect(addressPlan.getByText(/Fabric DHCP for bare-metal servers/)).toBeInTheDocument();
  });

  it('shows ICMP support as Unknown until the managers are resolved', async () => {
    const unresolved = networkClass();
    if (unresolved.status) {
      unresolved.status.securityGroupIcmp = undefined;
    }
    renderPage([unresolved]);

    const capabilities = within(await screen.findByLabelText('Capabilities'));
    expect(capabilities.getByText('Unknown')).toBeInTheDocument();
    expect(
      capabilities.getByText(/Shown once the network managers are resolved/),
    ).toBeInTheDocument();
  });

  it('explains why ICMP may be unavailable and shows east-west support', async () => {
    renderPage();

    const capabilities = within(await screen.findByLabelText('Capabilities'));
    expect(capabilities.getByText('Supported')).toBeInTheDocument();
    expect(capabilities.getByText('Not supported')).toBeInTheDocument();
    expect(
      capabilities.getByText(/A network manager does not support ICMP rules/),
    ).toBeInTheDocument();
  });

  it('has no actions', async () => {
    renderPage();

    await screen.findByText('EVPN on CloudVision');
    expect(screen.queryByRole('button', { name: /Create|Delete|Edit/ })).not.toBeInTheDocument();
  });

  it('shows an empty state without a network class', async () => {
    renderPage([]);

    expect(
      await screen.findByText(
        'No network class is configured. The installer creates it when it deploys OSAC.',
      ),
    ).toBeInTheDocument();
  });
});
