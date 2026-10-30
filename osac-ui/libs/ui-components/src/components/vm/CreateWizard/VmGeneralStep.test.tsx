import { screen, waitFor } from '@testing-library/react';
import { Formik, useFormikContext } from 'formik';
import { describe, expect, it } from 'vitest';

import { type Project, ProjectState } from '@osac/types';

import { type ComputeInstanceWizardValues } from './fields';
import { createEmptyComputeInstanceValues } from './payload';
import VmGeneralStep from './VmGeneralStep';
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

describe('VmGeneralStep', () => {
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
      { apiFixtures: { projects } },
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
