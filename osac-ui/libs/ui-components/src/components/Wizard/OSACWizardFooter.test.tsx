import { Code, ConnectError } from '@connectrpc/connect';
import { Wizard, WizardStep } from '@patternfly/react-core';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import { OSACWizardFooter } from './OSACWizardFooter';
import { renderWithProviders } from '../../test-utils/TestProviders';
import { FieldValidationProvider } from '../Form/FieldValidationContext';

const errorCases = [
  {
    label: 'InvalidArgument',
    code: Code.InvalidArgument,
    backendMessage: 'Volume size is invalid',
    expectedMessage: 'Volume size is invalid',
  },
  {
    label: 'AlreadyExists',
    code: Code.AlreadyExists,
    backendMessage: 'A volume with this name already exists',
    expectedMessage: 'A volume with this name already exists',
  },
  {
    label: 'PermissionDenied',
    code: Code.PermissionDenied,
    backendMessage: 'permission details from the backend',
    expectedMessage: 'You are not authorized to access this resource.',
  },
  {
    label: 'Internal',
    code: Code.Internal,
    backendMessage: 'internal stack details',
    expectedMessage: 'Unexpected error occurred',
  },
] as const;

type FormValues = { name: string };

const renderFooter = (
  error: unknown,
  onSubmit: () => Promise<void> = () => Promise.resolve(),
  isEdit = false,
) =>
  renderWithProviders(
    <Formik<FormValues> initialValues={{ name: '' }} onSubmit={onSubmit}>
      <FieldValidationProvider>
        <Wizard
          navAriaLabel="Wizard steps"
          footer={
            <OSACWizardFooter
              onCancel={vi.fn()}
              stepHasErrors={() => false}
              isEdit={isEdit}
              error={error}
            />
          }
        >
          <WizardStep id="review" name="Review">
            <div />
          </WizardStep>
        </Wizard>
      </FieldValidationProvider>
    </Formik>,
    { routerEntries: ['/'] },
  );

describe('OSACWizardFooter', () => {
  it.each(errorCases)(
    'maps $label to the create alert contract',
    ({ code, backendMessage, expectedMessage }) => {
      renderFooter(new ConnectError(backendMessage, code));

      const alert = screen
        .getByRole('heading', { name: 'Danger alert: Failed to create resource' })
        .closest('.pf-v6-c-alert');
      expect(alert).toHaveClass('pf-m-danger');
      expect(alert).toHaveTextContent('Failed to create resource');
      expect(alert).toHaveTextContent(expectedMessage);
    },
  );

  it('preserves backend error messages in edit mode', () => {
    const backendMessage = 'permission details from the backend';
    renderFooter(
      new ConnectError(backendMessage, Code.PermissionDenied),
      () => Promise.resolve(),
      true,
    );

    const alert = screen
      .getByRole('heading', { name: 'Danger alert: Failed to edit resource' })
      .closest('.pf-v6-c-alert');
    expect(alert).toHaveClass('pf-m-danger');
    expect(alert).toHaveTextContent(backendMessage);
    expect(alert).not.toHaveTextContent('You are not authorized to access this resource.');
  });

  it('disables the create button while Formik submission is pending', async () => {
    let resolveSubmit!: () => void;
    const onSubmit = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveSubmit = resolve;
        }),
    );
    const { user } = renderFooter(undefined, onSubmit);
    const submitButton = screen.getByRole('button', { name: 'Create' });

    await user.click(submitButton);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    expect(submitButton).toBeDisabled();

    resolveSubmit();
    await waitFor(() => expect(submitButton).toBeEnabled());
  });
});
