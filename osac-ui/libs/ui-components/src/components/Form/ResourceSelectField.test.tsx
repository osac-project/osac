import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik, type FormikErrors } from 'formik';
import { describe, expect, it, vi } from 'vitest';
import * as yup from 'yup';

import { type Tenant, TenantState, Tenants } from '@osac/types/private';

import {
  ResourceSelectField,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from './ResourceSelectField';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeTenant = (id: string, name: string): Tenant =>
  ({
    id,
    metadata: { name },
    spec: { domains: [`${name}.example.com`] },
    status: { state: TenantState.SYNCED },
  }) as Tenant;

const renderSelect = ({
  autoSelectSingleOption = false,
  initialValue = emptyResourceSelectValue(),
  tenants = [makeTenant('only-1', 'Only tenant')],
  transportOverrides,
  createOption,
}: {
  autoSelectSingleOption?: boolean;
  initialValue?: ResourceSelectValue;
  tenants?: Tenant[];
  transportOverrides?: MockTransportOverrides;
  createOption?: { label: string; onSelect: () => void };
} = {}) => {
  let latestErrors: FormikErrors<{ tenant: ResourceSelectValue }> = {};

  const view = renderWithProviders(
    <Formik
      initialValues={{ tenant: initialValue }}
      validationSchema={yup.object({
        tenant: yup.object({
          id: yup.string().required('Tenant is required'),
        }),
      })}
      onSubmit={() => undefined}
    >
      {({ validateForm, values }) => (
        <>
          <ResourceSelectField
            name="tenant"
            label="Tenant"
            fieldId="tenant"
            service={Tenants}
            isRequired
            autoSelectSingleOption={autoSelectSingleOption}
            createOption={createOption}
            placeholder="Select a tenant"
            loadErrorTitle="Failed to fetch tenants"
            emptyTitle="No registered tenants"
            emptyDescription="Register a tenant before creating and assigning an external IP pool."
          />
          <output aria-label="formik-value">{JSON.stringify(values.tenant)}</output>
          <button
            type="button"
            onClick={() => {
              void validateForm().then((errors) => {
                latestErrors = errors;
              });
            }}
          >
            Validate
          </button>
        </>
      )}
    </Formik>,
    { apiFixtures: { tenants }, transportOverrides },
  );

  return {
    ...view,
    getLatestErrors: () => latestErrors,
  };
};

describe('ResourceSelectField', () => {
  it('shows the placeholder on the toggle when no value is selected', async () => {
    renderSelect({
      autoSelectSingleOption: false,
      tenants: [makeTenant('t-1', 'acme'), makeTenant('t-2', 'globex')],
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Tenant/)).toHaveTextContent('Select a tenant');
    });
    expect(screen.getByLabelText('formik-value')).toHaveTextContent('{"id":"","name":""}');
  });

  it('stores id and name when the user selects an option', async () => {
    const { user } = renderSelect({
      tenants: [makeTenant('t-1', 'acme'), makeTenant('t-2', 'globex')],
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Tenant/)).not.toBeDisabled();
    });
    await user.click(screen.getByLabelText(/^Tenant/));
    await user.click(screen.getByRole('option', { name: 'globex' }));

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent(
        '{"id":"t-2","name":"globex"}',
      );
    });
    expect(screen.getByLabelText(/^Tenant/)).toHaveTextContent('globex');
  });

  it('auto-selects a single listed resource as id and name', async () => {
    renderSelect({ autoSelectSingleOption: true });

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent(
        '{"id":"only-1","name":"Only tenant"}',
      );
    });

    expect(screen.getByLabelText(/^Tenant/)).toHaveTextContent('Only tenant');
  });

  it('does not auto-select when multiple resources are listed', async () => {
    renderSelect({
      autoSelectSingleOption: true,
      tenants: [makeTenant('t-1', 'acme'), makeTenant('t-2', 'globex')],
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Tenant/)).toHaveTextContent('Select a tenant');
    });
    expect(screen.getByLabelText('formik-value')).toHaveTextContent('{"id":"","name":""}');
  });

  it('passes validation after auto-selecting the only resource', async () => {
    const { user, getLatestErrors } = renderSelect({ autoSelectSingleOption: true });

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent(
        '{"id":"only-1","name":"Only tenant"}',
      );
    });

    await user.click(screen.getByRole('button', { name: 'Validate' }));

    expect(getLatestErrors()).toEqual({});
  });

  it('reports a nested id error when nothing is selected', async () => {
    const { user, getLatestErrors } = renderSelect({
      tenants: [makeTenant('t-1', 'acme'), makeTenant('t-2', 'globex')],
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Tenant/)).not.toBeDisabled();
    });
    await user.click(screen.getByRole('button', { name: 'Validate' }));

    await waitFor(() => {
      expect(getLatestErrors()).toEqual({ tenant: { id: 'Tenant is required' } });
    });
  });

  it('shows an empty warning above a disabled select when the list has no resources', async () => {
    renderSelect({ tenants: [] });

    expect(await screen.findByText('No registered tenants')).toBeInTheDocument();
    expect(
      screen.getByText('Register a tenant before creating and assigning an external IP pool.'),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^Tenant/)).toBeDisabled();
  });

  it('offers resource creation when the list is empty', async () => {
    const onCreate = vi.fn();
    const { user } = renderSelect({
      tenants: [],
      createOption: { label: 'Create tenant', onSelect: onCreate },
    });

    await waitFor(() => expect(screen.getByLabelText(/^Tenant/)).not.toBeDisabled());
    await user.click(screen.getByLabelText(/^Tenant/));
    await user.click(screen.getByRole('option', { name: 'Create tenant' }));

    expect(onCreate).toHaveBeenCalledOnce();
    expect(screen.getByLabelText('formik-value')).toHaveTextContent('{"id":"","name":""}');
  });

  it('shows a load error above a disabled select when listing fails', async () => {
    renderSelect({
      transportOverrides: {
        onTenantList: () => {
          throw new ConnectError('tenants unavailable', Code.Unavailable);
        },
      },
    });

    expect(await screen.findByText('Failed to fetch tenants')).toBeInTheDocument();
    expect(screen.getByText('tenants unavailable')).toBeInTheDocument();
    expect(screen.queryByText('No registered tenants')).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^Tenant/)).toBeDisabled();
  });
});
