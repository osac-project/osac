import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import type { Tenant } from '@osac/types/private';

import { emptyResourceSelectValue } from './resourceSelectValue';
import TenantSelectField from './TenantSelectField';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeTenant = (id: string, name: string): Tenant =>
  ({
    id,
    metadata: { name },
  }) as Tenant;

describe('TenantSelectField', () => {
  it('stores the selected tenant id and name in the catalog form value', async () => {
    const { user } = renderWithProviders(
      <Formik
        initialValues={{ metadata: { tenant: emptyResourceSelectValue() } }}
        onSubmit={() => undefined}
      >
        {({ values }) => (
          <>
            <TenantSelectField />
            <output aria-label="selected-tenant">{JSON.stringify(values.metadata.tenant)}</output>
          </>
        )}
      </Formik>,
      { apiFixtures: { tenants: [makeTenant('tenant-1', 'Tenant One')] } },
    );

    await user.click(await screen.findByRole('button', { name: 'Tenant' }));
    await user.click(await screen.findByRole('option', { name: 'Tenant One' }));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-tenant')).toHaveTextContent(
        '{"id":"tenant-1","name":"Tenant One"}',
      );
    });
  });

  it('can render the selected tenant as disabled', async () => {
    renderWithProviders(
      <Formik
        initialValues={{
          metadata: { tenant: { id: 'tenant-1', name: 'Tenant One' } },
        }}
        onSubmit={() => undefined}
      >
        <TenantSelectField isDisabled />
      </Formik>,
      { apiFixtures: { tenants: [makeTenant('tenant-1', 'Tenant One')] } },
    );

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: 'Type to filter' })).toHaveValue('Tenant One');
    });
    expect(document.getElementById('catalog-item-tenant')).toHaveClass('pf-m-disabled');
  });
});
