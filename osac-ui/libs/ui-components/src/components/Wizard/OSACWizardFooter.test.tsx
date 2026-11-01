import { Code, ConnectError } from '@connectrpc/connect';
import { Wizard, WizardStep } from '@patternfly/react-core';
import { act, screen, waitFor } from '@testing-library/react';
import { Formik, type FormikErrors } from 'formik';
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
  validate?: () => Promise<FormikErrors<FormValues>>,
) =>
  renderWithProviders(
    <Formik<FormValues> initialValues={{ name: '' }} onSubmit={onSubmit} validate={validate}>
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
  it.each(['invalid', 'rejected'])(
    'releases the pending action after %s validation and retries',
    async (mode) => {
      let finish: (errors: FormikErrors<FormValues>) => void = () => undefined;
      let reject: (error: Error) => void = () => undefined;
      const pending = new Promise<FormikErrors<FormValues>>((resolve, rejectPromise) => {
        finish = resolve;
        reject = rejectPromise;
      });
      const validate = vi.fn(() => pending);
      const onSubmit = vi.fn(() => Promise.resolve());
      const { user } = renderFooter(undefined, onSubmit, false, validate);
      const button = screen.getByRole('button', { name: 'Create' });
      act(() => {
        button.click();
        button.click();
      });
      expect(button).toBeDisabled();
      expect(button).toHaveClass('pf-m-in-progress');
      await user.click(button);
      expect(validate).toHaveBeenCalledTimes(1);
      await act(async () => {
        if (mode === 'invalid') {
          finish({ name: 'required' });
        } else {
          reject(new Error('temporary validation failure'));
        }
        await pending.catch(() => undefined);
      });
      await screen.findByText('Fix the highlighted errors before continuing.');
      expect(button).toBeEnabled();
      expect(onSubmit).not.toHaveBeenCalled();
      validate.mockImplementation(() => Promise.resolve({}));
      button.focus();
      await user.keyboard('{Enter}');
      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
      await waitFor(() => expect(button).toBeEnabled());
    },
  );

  it('serializes pending Next navigation and releases Back and Cancel afterward', async () => {
    let finish: () => void = () => undefined;
    const pending = new Promise<void>((resolve) => {
      finish = resolve;
    });
    const validate = vi.fn(async () => {
      await pending;
      return {};
    });
    const onSubmit = vi.fn();
    const { user } = renderWithProviders(
      <Formik initialValues={{}} onSubmit={onSubmit} validate={validate}>
        <FieldValidationProvider>
          <Wizard
            footer={
              <OSACWizardFooter onCancel={vi.fn()} stepHasErrors={() => false} error={undefined} />
            }
          >
            <WizardStep id="general" name="General">
              <div>General fields</div>
            </WizardStep>
            <WizardStep id="review" name="Review">
              <div>Review fields</div>
            </WizardStep>
          </Wizard>
        </FieldValidationProvider>
      </Formik>,
    );
    const nextButton = screen.getByRole('button', { name: 'Next' });
    try {
      await user.click(nextButton);
      expect(nextButton).toBeDisabled();
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
      await user.click(nextButton);
      expect(validate).toHaveBeenCalledTimes(1);
      expect(screen.getByText('General fields')).toBeInTheDocument();
    } finally {
      await act(async () => {
        finish();
        await pending;
      });
    }
    await screen.findByText('Review fields');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Back' })).toBeEnabled());
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
    expect(onSubmit).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Back' }));
    await screen.findByText('General fields');
  });

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

  it.each([
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
  ])('maps $label to the edit alert contract', ({ code, backendMessage, expectedMessage }) => {
    renderFooter(new ConnectError(backendMessage, code), () => Promise.resolve(), true);

    const alert = screen
      .getByRole('heading', { name: 'Danger alert: Failed to edit resource' })
      .closest('.pf-v6-c-alert');
    expect(alert).toHaveClass('pf-m-danger');
    expect(alert).toHaveTextContent(expectedMessage);
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
