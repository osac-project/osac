import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import UserDataField from './UserDataField';
import { renderWithProviders } from '../../test-utils/TestProviders';

const renderUserDataField = () =>
  renderWithProviders(
    <Formik initialValues={{ source: 'inline', secret: '', value: '' }} onSubmit={() => undefined}>
      <UserDataField
        sourceName="source"
        secretRefName="secret"
        inlineName="value"
        projectName="project-a"
      />
    </Formik>,
    { apiFixtures: { secrets: [] } },
  );

describe('UserDataField', () => {
  it('renders source choices as selectable cards', () => {
    renderUserDataField();

    expect(screen.getByText('Paste cloud-init user data')).toBeInTheDocument();
    expect(screen.getByText('Use a USER_DATA secret from this project')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Enter value' })).toBeChecked();
  });

  it('switches between inline value and secret cards', async () => {
    const { user } = renderUserDataField();

    await user.click(screen.getByRole('radio', { name: 'Select secret' }));

    expect(screen.getByRole('radio', { name: 'Select secret' })).toBeChecked();
    expect(
      await screen.findByRole('button', { name: 'No secret available' }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole('radio', { name: 'Enter value' }));

    expect(screen.getByRole('radio', { name: 'Enter value' })).toBeChecked();
    expect(screen.getByRole('textbox', { name: 'User data' })).toBeInTheDocument();
  });
});
