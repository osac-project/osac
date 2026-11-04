import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SecretLocalReferenceSchema, UserDataSourceSchema } from '@osac/types';

import UserDataDetailValue from './UserDataDetailValue';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('UserDataDetailValue', () => {
  it('renders the supplied empty value when user data is not configured', () => {
    renderWithProviders(<UserDataDetailValue emptyValue="—" />);

    expect(screen.getByText('—')).toBeInTheDocument();
  });

  it('expands inline user data and preserves whitespace', async () => {
    const userData = '  #cloud-config\nusers: []\n';
    const { user } = renderWithProviders(
      <UserDataDetailValue
        userData={create(UserDataSourceSchema, { source: { case: 'inline', value: userData } })}
        emptyValue="—"
      />,
    );

    expect(screen.getByRole('button', { name: 'Expand user data' })).toHaveAttribute(
      'aria-expanded',
      'false',
    );
    expect(screen.getByText(/#cloud-config/)).not.toBeVisible();

    await user.click(screen.getByRole('button', { name: 'Expand user data' }));

    expect(screen.getByRole('button', { name: 'Collapse user data' })).toHaveAttribute(
      'aria-expanded',
      'true',
    );
    expect(screen.getByText(/#cloud-config/).textContent).toBe(userData);
  });

  it('links Secret-backed user data to the Secret resource', () => {
    renderWithProviders(
      <UserDataDetailValue
        userData={create(UserDataSourceSchema, {
          source: {
            case: 'secret',
            value: create(SecretLocalReferenceSchema, {
              id: 'secret-1',
              name: 'cloud-init-secret',
            }),
          },
        })}
        emptyValue="—"
      />,
    );

    expect(screen.getByRole('link', { name: 'cloud-init-secret' })).toHaveAttribute(
      'href',
      '/secrets/secret-1',
    );
    expect(screen.queryByRole('button', { name: /user data/i })).not.toBeInTheDocument();
  });

  it('shows a Secret name without a link when its ID is unavailable', () => {
    renderWithProviders(
      <UserDataDetailValue
        userData={create(UserDataSourceSchema, {
          source: {
            case: 'secret',
            value: create(SecretLocalReferenceSchema, { name: 'cloud-init-secret' }),
          },
        })}
        emptyValue="—"
      />,
    );

    expect(screen.getByText('cloud-init-secret')).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /user data/i })).not.toBeInTheDocument();
  });
});
