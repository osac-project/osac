import { create } from '@bufbuild/protobuf';
import type { Transport } from '@connectrpc/connect';
import type { RenderOptions } from '@testing-library/react';
import { screen, waitFor, within } from '@testing-library/react';
import { UserEvent } from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import {
  type ClusterCatalogItem,
  type ComputeInstanceCatalogItem,
  type Secret,
  SecretType,
} from '@osac/types';
import {
  ClusterTemplateReferenceSchema,
  ComputeInstanceTemplateReferenceSchema,
  HostTypeReferenceSchema,
  InstanceTypeState,
  SecurityGroupState,
  StorageTierSchema,
  StorageTierState,
  SubnetState,
  VirtualNetworkLocalReferenceSchema,
  VirtualNetworkState,
} from '@osac/types';

import type { CatalogProvisionKind } from './catalogFieldDefinition';
import type { CatalogProvisionPayload } from './catalogProvisionTypes';
import { CatalogProvisionWizard } from './CatalogProvisionWizard';
import { SessionProvider } from '../../hooks/use-session';
import type {
  MockApiFixtures,
  MockTransportOverrides,
} from '../../test-utils/createMockConnectTransport';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const fillClusterGeneralStep = async (user: UserEvent, name: string) => {
  const nameInput = screen.getByLabelText(/^Name/);
  await user.clear(nameInput);
  await user.type(nameInput, name);
  await user.click(screen.getByLabelText(/Pull secret secret/));
  await user.click(screen.getByRole('option', { name: 'pull-secret' }));
};

const fillGeneralStep = async (user: UserEvent, name: string) => {
  const nameInput = screen.getByLabelText(/^Name/);
  await user.clear(nameInput);
  await user.type(nameInput, name);
};

export const clickWizardNext = async (user: UserEvent) => {
  const [nextButton] = screen.getAllByRole('button', { name: 'Next' });
  await user.click(nextButton);
};

const catalogItemGroup = () => screen.getByRole('radiogroup', { name: 'Catalog item' });

const expectCatalogItemVisible = async (title: string) => {
  await waitFor(() => {
    expect(within(catalogItemGroup()).getByText(title)).toBeInTheDocument();
  });
};

const expectCatalogItemSelected = (title: string) => {
  const titleNode = within(catalogItemGroup()).getByText(title);
  const card = titleNode.closest<HTMLElement>('.pf-v6-c-card');
  if (!card) {
    throw new Error(`Catalog card not found for ${title}`);
  }
  expect(within(card).getByRole('radio')).toBeChecked();
};

const selectCatalogItem = async (user: UserEvent, title: string) => {
  await expectCatalogItemVisible(title);
  const titleNode = within(catalogItemGroup()).getByText(title);
  const card = titleNode.closest<HTMLElement>('.pf-v6-c-card');
  if (!card) {
    throw new Error(`Catalog card not found for ${title}`);
  }
  await user.click(within(card).getByRole('radio'));
};

const clickWizardBack = async (user: UserEvent) => {
  const [backButton] = screen.getAllByRole('button', { name: 'Back' });
  await user.click(backButton);
};

const clickWizardCancel = async (user: UserEvent) => {
  const [cancelButton] = screen.getAllByRole('button', { name: 'Cancel' });
  await user.click(cancelButton);
};

const advanceToConfigurationStep = async (
  user: UserEvent,
  vmName: string,
  catalogItemTitle: string,
) => {
  await selectCatalogItem(user, catalogItemTitle);
  await clickWizardNext(user);
  await waitFor(() => {
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
  });
  await fillGeneralStep(user, vmName);
  await clickWizardNext(user);
  await waitFor(() => {
    expect(screen.getByLabelText(/^Instance type/)).toBeInTheDocument();
  });
};

const waitForConfigurationReady = async () => {
  await waitFor(() => {
    expect(screen.getByLabelText(/^Instance type/)).not.toBeDisabled();
    expect(screen.getByLabelText(/^Instance type/)).toHaveTextContent('standard-4-8');
  });
};

const advanceToStorageStep = async (user: UserEvent, catalogItemTitle: string) => {
  await advanceToConfigurationStep(user, 'web-01', catalogItemTitle);
  await waitForConfigurationReady();
  await clickWizardNext(user);
  await waitFor(() => {
    expect(screen.getByLabelText(/Boot disk/)).toBeInTheDocument();
  });
};

const fillStorageStep = async (user: UserEvent) => {
  const bootDisk = screen.getByLabelText<HTMLInputElement>(/Boot disk/);
  if (!bootDisk.value) {
    await user.clear(bootDisk);
    await user.type(bootDisk, '40');
  }
  const storageTierToggle = (await screen.findAllByLabelText(/^Storage tier/))[0];
  await waitFor(() => {
    expect(storageTierToggle).not.toBeDisabled();
  });
  if (!storageTierToggle.textContent?.includes('fast')) {
    await user.click(storageTierToggle);
    await user.click(await screen.findByRole('option', { name: 'fast' }));
  }
};

const advanceToNetworkingStep = async (user: UserEvent, catalogItemTitle: string) => {
  await advanceToStorageStep(user, catalogItemTitle);
  await fillStorageStep(user);
  await clickWizardNext(user);
  await waitFor(() => {
    expect(screen.getByLabelText(/^Virtual network/)).toBeInTheDocument();
  });
};

const selectNetworkingPickers = async (user: UserEvent) => {
  await waitFor(() => {
    expect(screen.getByLabelText(/^Virtual network/)).not.toBeDisabled();
    expect(screen.getByLabelText(/^Virtual network/)).toHaveTextContent('tenant-vn');
    expect(screen.getByLabelText(/^Subnet/)).toHaveTextContent('tenant-subnet');
    expect(screen.getByText('default-sg')).toBeInTheDocument();
  });

  const sgToggle = screen.getByLabelText(/^Security groups/);
  if (sgToggle.textContent === 'Select security groups') {
    await user.click(sgToggle);
    await user.click(screen.getByRole('menuitemcheckbox', { name: /default-sg/ }));
  }

  await waitFor(() => {
    expect(screen.getByLabelText(/^Security groups/)).not.toHaveTextContent(
      'Select security groups',
    );
  });
};

const advanceToReviewStep = async (user: UserEvent, catalogItemTitle: string) => {
  await advanceToNetworkingStep(user, catalogItemTitle);
  await selectNetworkingPickers(user);
  await clickWizardNext(user);
  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
  });
};

const expectValidationAlert = async () => {
  await waitFor(() => {
    expect(screen.getByText('Fix the highlighted errors before continuing.')).toBeInTheDocument();
  });
};

const getCancelModal = () => {
  const dialog = screen.getByRole('dialog');
  return within(dialog);
};

const vmCatalogItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-rhel-9',
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  template: create(ComputeInstanceTemplateReferenceSchema, { id: 'tpl-rhel-9' }),
  published: true,
  templateParameters: {},
};

const clusterCatalogItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-openshift-4',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-openshift-4',
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'OpenShift 4 cluster',
  description: 'Standard OpenShift cluster offering',
  template: create(ClusterTemplateReferenceSchema, { id: 'tpl-openshift-4' }),
  published: true,
  templateParameters: {},
};

const catalogItemWithDistinctDefaults: ComputeInstanceCatalogItem = {
  ...vmCatalogItem,
  templateParameters: {},
};

const apiFixtures: MockApiFixtures = {
  secrets: [
    {
      $typeName: 'osac.public.v1.Secret',
      id: 'pull-secret-id',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        annotations: {},
        creator: '',
        labels: {},
        name: 'pull-secret',
        project: '',
        tenant: '',
        version: 1,
      },
      data: {},
      type: SecretType.PULL_SECRET,
    } as Secret,
  ],
  catalogItems: [vmCatalogItem],
  clusterCatalogItems: [clusterCatalogItem],
  clusterTemplates: [
    {
      $typeName: 'osac.public.v1.ClusterTemplate',
      id: 'tpl-openshift-4',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'tpl-openshift-4',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      nodeSets: {
        compute: {
          $typeName: 'osac.public.v1.ClusterTemplateNodeSet',
          hostType: create(HostTypeReferenceSchema, { id: 'acme_1tb' }),
          size: 3,
        },
      },
      description: '',
      parameters: [],
      title: '',
    },
  ],
  hostTypes: [
    {
      $typeName: 'osac.public.v1.HostType',
      id: 'acme_1tb',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'acme_1tb',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      title: 'ACME 1TB',
      description: '',
      interfaces: [],
    },
    {
      $typeName: 'osac.public.v1.HostType',
      id: 'acme_1tb_h100',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'acme_1tb_h100',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      title: 'ACME 1TB H100',
      description: '',
      interfaces: [],
    },
  ],
  virtualNetworks: [
    {
      $typeName: 'osac.public.v1.VirtualNetwork',
      id: 'vn-1',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'tenant-vn',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      status: {
        $typeName: 'osac.public.v1.VirtualNetworkStatus',
        state: VirtualNetworkState.READY,
      },
    },
  ],
  subnets: [
    {
      $typeName: 'osac.public.v1.Subnet',
      id: 'subnet-1',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'tenant-subnet',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      spec: {
        $typeName: 'osac.public.v1.SubnetSpec',
        virtualNetwork: create(VirtualNetworkLocalReferenceSchema, { id: 'vn-1' }),
      },
      status: {
        $typeName: 'osac.public.v1.SubnetStatus',
        state: SubnetState.READY,
      },
    },
  ],
  securityGroups: [
    {
      $typeName: 'osac.public.v1.SecurityGroup',
      id: 'sg-1',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'default-sg',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      spec: {
        $typeName: 'osac.public.v1.SecurityGroupSpec',
        virtualNetwork: create(VirtualNetworkLocalReferenceSchema, { id: 'vn-1' }),
        egress: [],
        ingress: [],
      },
      status: {
        $typeName: 'osac.public.v1.SecurityGroupStatus',
        state: SecurityGroupState.READY,
      },
    },
  ],
  instanceTypes: [
    {
      $typeName: 'osac.public.v1.InstanceType',
      id: 'standard-4-8',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'standard-4-8',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      spec: {
        $typeName: 'osac.public.v1.InstanceTypeSpec',
        vcpus: 4,
        memoryGib: 8,
        state: InstanceTypeState.ACTIVE,
        description: '',
      },
    },
  ],
  publicStorageTiers: [
    create(StorageTierSchema, {
      id: 'id-fast',
      metadata: { name: 'fast', displayName: 'fast' },
      status: { state: StorageTierState.ACTIVE },
    }),
  ],
};

type RenderWizardOptions = {
  kind?: CatalogProvisionKind;
  initialCatalogItemId?: string;
  apiFixtures?: MockApiFixtures;
  transport?: Transport;
  transportOverrides?: MockTransportOverrides;
  onProvision?: (payload: CatalogProvisionPayload) => void | Promise<void>;
  onClosed?: () => void;
} & Omit<RenderOptions, 'wrapper'>;

const renderWizard = (options: RenderWizardOptions = {}) => {
  const onProvision = options.onProvision ?? vi.fn();
  const onClosed = options.onClosed ?? vi.fn();

  const result = renderWithProviders(
    <SessionProvider role="tenant-user" username="test-user" tenantId="test-tenant">
      <CatalogProvisionWizard
        kind={options.kind || 'compute_instance'}
        initialCatalogItemId={options.initialCatalogItemId}
        onProvision={onProvision}
        onClosed={onClosed}
      />
    </SessionProvider>,
    {
      apiFixtures: options.apiFixtures ?? apiFixtures,
      transport: options.transport,
      transportOverrides: options.transportOverrides,
    },
  );

  return { ...result, onProvision, onClosed };
};

const expectConfigurationDefaults = async () => {
  await waitFor(() => {
    expect(screen.getByLabelText(/^Instance type/)).toBeInTheDocument();
  });
};

describe('CatalogProvisionWizard', () => {
  it('blocks Next on catalog step when no catalog item is selected', async () => {
    const { user } = renderWizard();

    await expectCatalogItemVisible('catalog-rhel-9');

    await clickWizardNext(user);
    await expectValidationAlert();
    expect(screen.getByText('Select a catalog item')).toBeInTheDocument();
    await expectCatalogItemVisible('catalog-rhel-9');
  });

  it('blocks Next on general step when name is empty', async () => {
    const { user } = renderWizard();

    await selectCatalogItem(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardNext(user);
    await waitFor(() => {
      expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    });

    await clickWizardNext(user);
    await expectValidationAlert();
    expect(screen.getByText('Name is required')).toBeInTheDocument();
  });

  it('prefills configuration fields from catalog item defaults after selection', async () => {
    const { user } = renderWizard({
      apiFixtures: { ...apiFixtures, catalogItems: [catalogItemWithDistinctDefaults] },
    });

    await selectCatalogItem(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardNext(user);
    await fillGeneralStep(user, 'web-01');
    await clickWizardNext(user);

    await expectConfigurationDefaults();
  });

  it('prefills configuration fields when deep-linked before catalog items finish loading', async () => {
    let releaseCatalogFetch!: () => void;
    const catalogFetchGate = new Promise<void>((resolve) => {
      releaseCatalogFetch = resolve;
    });

    const catalogApiFixtures = {
      ...apiFixtures,
      catalogItems: [catalogItemWithDistinctDefaults],
    };
    const baseTransport = createMockConnectTransport(catalogApiFixtures);

    const originalUnary = baseTransport.unary.bind(baseTransport);
    const unary: Transport['unary'] = async (...args) => {
      const method = args[0];
      if (method.parent.typeName.endsWith('ComputeInstanceCatalogItems')) {
        await catalogFetchGate;
      }
      return originalUnary(...args);
    };
    const gatedTransport: Transport = { ...baseTransport, unary };

    const { user } = renderWizard({
      initialCatalogItemId: vmCatalogItem.id,
      transport: gatedTransport,
    });

    await clickWizardNext(user);
    await fillGeneralStep(user, 'web-01');
    await clickWizardNext(user);

    releaseCatalogFetch();

    await expectConfigurationDefaults();
  });

  it('highlights the Name field with a required error when Next is clicked without entering a name', async () => {
    const { user } = renderWizard();

    await selectCatalogItem(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardNext(user);

    const nameInput = await screen.findByLabelText(/^Name/);
    expect(nameInput).not.toHaveAttribute('aria-invalid', 'true');

    await clickWizardNext(user);

    await expectValidationAlert();

    expect(nameInput).toHaveAttribute('aria-invalid', 'true');
    expect(nameInput).toHaveAccessibleDescription(/Name is required/);

    const nameError = document.getElementById('metadata-name-helper-error');
    expect(nameError).toHaveTextContent('Name is required');
    expect(nameInput).toHaveAttribute('aria-describedby', 'metadata-name-helper-error');

    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Instance type/)).not.toBeInTheDocument();
  });

  it('closes immediately on Cancel when the wizard is pristine', async () => {
    const onClosed = vi.fn();
    const { user } = renderWizard({ onClosed });

    await waitFor(() => {
      expect(screen.getAllByRole('button', { name: 'Cancel' }).length).toBeGreaterThan(0);
    });

    await clickWizardCancel(user);
    expect(onClosed).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows discard confirmation on Cancel after selecting a catalog item', async () => {
    const onClosed = vi.fn();
    const { user } = renderWizard({ onClosed });

    await selectCatalogItem(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardCancel(user);

    const modal = getCancelModal();
    expect(modal.getByText('Discard wizard progress?')).toBeInTheDocument();
    expect(onClosed).not.toHaveBeenCalled();
  });

  it('keeps wizard open when Stay editing is chosen on the discard modal', async () => {
    const onClosed = vi.fn();
    const { user } = renderWizard({ onClosed });

    await selectCatalogItem(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardCancel(user);

    const modal = getCancelModal();
    await user.click(modal.getByRole('button', { name: 'Keep editing' }));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(onClosed).not.toHaveBeenCalled();
    expectCatalogItemSelected('catalog-rhel-9');
  });

  it('discards and closes when Discard is confirmed', async () => {
    const onClosed = vi.fn();
    const { user } = renderWizard({ onClosed });

    await selectCatalogItem(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardCancel(user);

    const modal = getCancelModal();
    await user.click(modal.getByRole('button', { name: 'Discard and close' }));

    expect(onClosed).toHaveBeenCalledTimes(1);
  });

  it('preserves general step values after navigating back from configuration', async () => {
    const { user } = renderWizard();

    await advanceToConfigurationStep(user, 'persisted-vm', vmCatalogItem.metadata?.name ?? '');
    await clickWizardBack(user);

    await waitFor(() => {
      expect(screen.getByLabelText(/^Name/)).toHaveValue('persisted-vm');
    });
  });

  it('preserves configuration values after navigating back from networking', async () => {
    const { user } = renderWizard();

    await advanceToNetworkingStep(user, vmCatalogItem.metadata?.name ?? '');
    await clickWizardBack(user);
    await waitFor(() => {
      expect(screen.getByLabelText(/Boot disk/)).toBeInTheDocument();
    });
    await clickWizardBack(user);

    await waitFor(() => {
      expect(screen.getByLabelText(/^Instance type/)).toBeInTheDocument();
    });
  });

  it('shows only active instance types when deprecated and active share a name', async () => {
    const { user } = renderWizard({
      apiFixtures: {
        ...apiFixtures,
        instanceTypes: [
          {
            $typeName: 'osac.public.v1.InstanceType',
            id: 'standard-deprecated',
            metadata: {
              $typeName: 'osac.public.v1.Metadata',
              displayName: '',
              description: '',
              name: 'standard-4-8',
              annotations: {},
              creator: 'foo',
              labels: {},
              project: 'foo',
              tenant: 'foo',
              version: 1,
            },
            spec: {
              $typeName: 'osac.public.v1.InstanceTypeSpec',
              vcpus: 4,
              memoryGib: 8,
              state: InstanceTypeState.DEPRECATED,
              description: '',
            },
          },
          {
            $typeName: 'osac.public.v1.InstanceType',
            id: 'standard-active',
            metadata: {
              $typeName: 'osac.public.v1.Metadata',
              displayName: '',
              description: '',
              name: 'standard-4-8',
              annotations: {},
              creator: 'foo',
              labels: {},
              project: 'foo',
              tenant: 'foo',
              version: 1,
            },
            spec: {
              $typeName: 'osac.public.v1.InstanceTypeSpec',
              vcpus: 4,
              memoryGib: 8,
              state: InstanceTypeState.ACTIVE,
              description: '',
            },
          },
        ],
      },
    });
    await advanceToConfigurationStep(user, 'web-01', vmCatalogItem.metadata?.name ?? '');

    await waitFor(() => {
      expect(screen.getByLabelText(/^Instance type/)).not.toBeDisabled();
    });

    await user.click(screen.getByLabelText(/^Instance type/));

    await waitFor(() => {
      const instanceTypeOptions = screen.getAllByRole('option');
      expect(instanceTypeOptions).toHaveLength(1);
      expect(instanceTypeOptions[0]).toHaveTextContent('standard-4-8');
    });
  });

  it('shows only ready virtual networks when pending and ready share a name', async () => {
    const { user } = renderWizard({
      apiFixtures: {
        ...apiFixtures,
        virtualNetworks: [
          {
            $typeName: 'osac.public.v1.VirtualNetwork',
            id: 'vn-pending',
            metadata: {
              $typeName: 'osac.public.v1.Metadata',
              displayName: '',
              description: '',
              name: 'tenant-vn',
              annotations: {},
              creator: 'foo',
              labels: {},
              project: 'foo',
              tenant: 'foo',
              version: 1,
            },
            status: {
              $typeName: 'osac.public.v1.VirtualNetworkStatus',
              state: VirtualNetworkState.PENDING,
            },
          },
          {
            $typeName: 'osac.public.v1.VirtualNetwork',
            id: 'vn-ready',
            metadata: {
              $typeName: 'osac.public.v1.Metadata',
              displayName: '',
              description: '',
              name: 'tenant-vn',
              annotations: {},
              creator: 'foo',
              labels: {},
              project: 'foo',
              tenant: 'foo',
              version: 1,
            },
            status: {
              $typeName: 'osac.public.v1.VirtualNetworkStatus',
              state: VirtualNetworkState.READY,
            },
          },
        ],
        subnets: [
          {
            $typeName: 'osac.public.v1.Subnet',
            id: 'subnet-1',
            metadata: {
              $typeName: 'osac.public.v1.Metadata',
              displayName: '',
              description: '',
              name: 'tenant-subnet',
              annotations: {},
              creator: 'foo',
              labels: {},
              project: 'foo',
              tenant: 'foo',
              version: 1,
            },
            spec: {
              $typeName: 'osac.public.v1.SubnetSpec',
              virtualNetwork: create(VirtualNetworkLocalReferenceSchema, { id: 'vn-ready' }),
            },
            status: {
              $typeName: 'osac.public.v1.SubnetStatus',
              state: SubnetState.READY,
            },
          },
        ],
        securityGroups: [
          {
            $typeName: 'osac.public.v1.SecurityGroup',
            id: 'sg-1',
            metadata: {
              $typeName: 'osac.public.v1.Metadata',
              displayName: '',
              description: '',
              name: 'default-sg',
              annotations: {},
              creator: 'foo',
              labels: {},
              project: 'foo',
              tenant: 'foo',
              version: 1,
            },
            spec: {
              $typeName: 'osac.public.v1.SecurityGroupSpec',
              virtualNetwork: create(VirtualNetworkLocalReferenceSchema, { id: 'vn-ready' }),
              egress: [],
              ingress: [],
            },
            status: {
              $typeName: 'osac.public.v1.SecurityGroupStatus',
              state: SecurityGroupState.READY,
            },
          },
        ],
      },
    });
    await advanceToNetworkingStep(user, vmCatalogItem.metadata?.name ?? '');

    await waitFor(() => {
      expect(screen.getByLabelText(/^Virtual network/)).toHaveTextContent('tenant-vn');
    });

    await user.click(screen.getByLabelText(/^Virtual network/));

    await waitFor(() => {
      const networkOptions = screen.getAllByRole('option');
      expect(networkOptions).toHaveLength(1);
      expect(networkOptions[0]).toHaveTextContent('tenant-vn');
    });
  });

  it('shows virtual network and subnet names on the review step', async () => {
    const { user } = renderWizard();

    await advanceToReviewStep(user, vmCatalogItem.metadata?.name ?? '');

    await waitFor(() => {
      expect(screen.getByText('tenant-vn')).toBeInTheDocument();
      expect(screen.getByText('tenant-subnet')).toBeInTheDocument();
    });
  });

  it('shows security group names on the review step', async () => {
    const { user } = renderWizard();

    await advanceToReviewStep(user, vmCatalogItem.metadata?.name ?? '');

    await waitFor(() => {
      expect(screen.getByText('default-sg')).toBeInTheDocument();
    });
  });

  it('shows instance type on the review step', async () => {
    const { user } = renderWizard();

    await advanceToReviewStep(user, vmCatalogItem.metadata?.name ?? '');

    await waitFor(() => {
      expect(screen.getByText('standard-4-8 — 4 vCPUs, 8 GiB')).toBeInTheDocument();
    });
  });

  it('submits create payload from review and calls onProvision', async () => {
    const onProvision = vi.fn().mockResolvedValue(undefined);
    const { user } = renderWizard({ onProvision });

    await advanceToReviewStep(user, vmCatalogItem.metadata?.name ?? '');

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(onProvision).toHaveBeenCalledTimes(1);
    });

    expect(onProvision.mock.calls[0][0]).toMatchObject({
      metadata: { name: 'web-01' },
      spec: {
        instanceType: { id: 'standard-4-8' },
      },
    });
    expect(onProvision.mock.calls[0][0]).not.toHaveProperty('spec.vcpus');
    expect(onProvision.mock.calls[0][0]).not.toHaveProperty('spec.memoryGib');
    expect(onProvision.mock.calls[0][0]).toHaveProperty('spec.networkAttachments', [
      { subnet: { id: 'subnet-1' }, securityGroups: [{ id: 'sg-1' }] },
    ]);
  });

  it('surfaces provision errors on review without clearing form values', async () => {
    const onProvision = vi.fn().mockRejectedValue(new Error('provision failed'));
    const { user } = renderWizard({ onProvision });

    await advanceToReviewStep(user, vmCatalogItem.metadata?.name ?? '');

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('provision failed')).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
  });

  it('includes a Storage step in the compute instance wizard and omits it for clusters', async () => {
    const { unmount } = renderWizard();
    await expectCatalogItemVisible('catalog-rhel-9');
    expect(screen.getByRole('button', { name: 'Storage' })).toBeInTheDocument();
    unmount();

    renderWizard({ kind: 'cluster' });
    await expectCatalogItemVisible('catalog-openshift-4');
    expect(screen.queryByRole('button', { name: 'Storage' })).not.toBeInTheDocument();
  });

  it('moves boot disk controls off Configuration and onto the Storage step', async () => {
    const { user } = renderWizard();

    await advanceToConfigurationStep(user, 'web-01', vmCatalogItem.metadata?.name ?? '');
    expect(screen.getByLabelText(/^Instance type/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/Boot disk/)).not.toBeInTheDocument();

    await waitForConfigurationReady();
    await clickWizardNext(user);

    await waitFor(() => {
      expect(screen.getByLabelText(/Boot disk/)).toBeInTheDocument();
    });
    expect(screen.getByText('Storage tier')).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Instance type/)).not.toBeInTheDocument();
  });

  it('blocks Next on the Storage step until the boot disk size is valid', async () => {
    const { user } = renderWizard();

    await advanceToStorageStep(user, vmCatalogItem.metadata?.name ?? '');
    const bootDisk = screen.getByLabelText<HTMLInputElement>(/Boot disk/);
    await user.clear(bootDisk);
    await clickWizardNext(user);

    await expectValidationAlert();
    expect(screen.getByLabelText(/Boot disk/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Virtual network/)).not.toBeInTheDocument();
  });

  it('blocks Next on cluster general step when name is invalid', async () => {
    const { user } = renderWizard({ kind: 'cluster' });

    await selectCatalogItem(user, clusterCatalogItem.metadata?.name ?? '');
    await clickWizardNext(user);
    await fillClusterGeneralStep(user, 'MyCluster');
    await clickWizardNext(user);

    await expectValidationAlert();
    expect(
      screen.getByText(
        'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
      ),
    ).toBeInTheDocument();
  });
});
