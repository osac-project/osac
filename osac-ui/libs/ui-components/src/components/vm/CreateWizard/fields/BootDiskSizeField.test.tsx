import { fireEvent, screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import BootDiskSizeField from './BootDiskSizeField';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

describe('BootDiskSizeField', () => {
  it('updates the Formik boot disk size', async () => {
    renderWithProviders(
      <Formik initialValues={{ spec: { bootDisk: { sizeGib: '40' } } }} onSubmit={() => undefined}>
        {({ values }) => (
          <>
            <BootDiskSizeField name="spec.bootDisk.sizeGib" />
            <output aria-label="size">{values.spec.bootDisk.sizeGib}</output>
          </>
        )}
      </Formik>,
    );

    const input = screen.getByRole('spinbutton', { name: /^Boot disk/ });
    fireEvent.change(input, { target: { value: '80' } });

    await waitFor(() => {
      expect(screen.getByLabelText('size')).toHaveTextContent('80');
    });
  });
});
