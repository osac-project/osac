import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik, useFormikContext } from 'formik';
import { describe, expect, it } from 'vitest';

import {
  ComputeInstanceCatalogItemSchema,
  DiskImageLifecycle,
  DiskImageSchema,
  type Project,
  ProjectState,
} from '@osac/types';

import { type ComputeInstanceWizardValues, getComputeInstanceInitialValues } from './fields';
import { createEmptyComputeInstanceValues } from './payload';
import VmGeneralStep from './VmGeneralStep';
import {
  expectResourceSelectDisabled,
  getResourceSelectCombobox,
} from '../../../test-utils/resourceSelect';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const makeProject = (id: string, name: string): Project =>
  ({
    id,
    metadata: { name, project: '' },
    spec: { title: name },
    status: { state: ProjectState.ACTIVE },
  }) as Project;

const projects = [makeProject('p-a', 'project-a'), makeProject('p-b', 'project-b')];

const SecretValuesReadout = () => {
  const { values } = useFormikContext<ComputeInstanceWizardValues>();
  return (
    <>
      <output aria-label="ssh-key">{values.spec.sshKey.value}</output>
      <output aria-label="user-data-secret">{values.spec.userDataSecret.value}</output>
    </>
  );
};

const diskImages = [
  create(DiskImageSchema, {
    id: 'disk-image-fedora',
    metadata: { name: 'fedora' },
    spec: { lifecycle: DiskImageLifecycle.AVAILABLE, sourceRef: 'quay.io/fedora:latest' },
  }),
  create(DiskImageSchema, {
    id: 'disk-image-rhel',
    metadata: { name: 'rhel' },
    spec: { lifecycle: DiskImageLifecycle.AVAILABLE, sourceRef: 'quay.io/rhel:latest' },
  }),
];

describe('VmGeneralStep', () => {
  it('allows the user to select a disk image from the available images', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyComputeInstanceValues()} onSubmit={() => undefined}>
        <VmGeneralStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
      },
    );

    const diskImageSelect = await screen.findByLabelText(/^Disk image/);
    await user.click(diskImageSelect);

    expect(screen.getByRole('option', { name: /fedora/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /rhel/ })).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /rhel/ }));

    await waitFor(() => expect(getResourceSelectCombobox(/^Disk image/)).toHaveValue('rhel'));
  });

  it('shows the catalog item disk image when the wizard opens', async () => {
    const catalogItem = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-rhel-9',
      fields: {
        diskImage: {
          behavior: {
            case: 'locked',
            value: { id: 'disk-image-rhel', name: 'rhel' },
          },
        },
      },
    });

    renderWithProviders(
      <Formik
        initialValues={getComputeInstanceInitialValues(catalogItem)}
        onSubmit={() => undefined}
      >
        <VmGeneralStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
      },
    );

    await screen.findByLabelText(/^Disk image/);
    await waitFor(() => expect(getResourceSelectCombobox(/^Disk image/)).toHaveValue('rhel'));
    expectResourceSelectDisabled(/^Disk image/);
  });

  it('clears unlocked SSH key and user-data secret references when the project changes', async () => {
    const initialValues: ComputeInstanceWizardValues = {
      ...createEmptyComputeInstanceValues(),
      metadata: { name: '', project: 'project-a' },
      spec: {
        ...createEmptyComputeInstanceValues().spec,
        sshKey: provisioningFieldValue('ssh-secret-a'),
        userDataSecret: provisioningFieldValue('userdata-secret-a'),
      },
    };

    const { user } = renderWithProviders(
      <Formik initialValues={initialValues} onSubmit={() => undefined}>
        <>
          <VmGeneralStep />
          <SecretValuesReadout />
        </>
      </Formik>,
      { apiFixtures: { projects, diskImages: [] } },
    );

    expect(screen.getByLabelText('ssh-key')).toHaveTextContent('ssh-secret-a');
    expect(screen.getByLabelText('user-data-secret')).toHaveTextContent('userdata-secret-a');

    const projectSelect = await screen.findByLabelText(/^Project/);
    await user.click(projectSelect);
    await user.click(screen.getByRole('option', { name: 'project-b' }));

    await waitFor(() => {
      expect(screen.getByLabelText('ssh-key')).toHaveTextContent('');
      expect(screen.getByLabelText('user-data-secret')).toHaveTextContent('');
    });
  });
});
