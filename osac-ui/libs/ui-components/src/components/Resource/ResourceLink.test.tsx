import { MemoryRouter } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ResourceLink, { type ResourceLinkProps } from './ResourceLink';

const renderResourceLink = (props: ResourceLinkProps) =>
  render(
    <MemoryRouter>
      <ResourceLink {...props} />
    </MemoryRouter>,
  );

describe('ResourceLink', () => {
  it('renders the resource name at the resource detail path', () => {
    renderResourceLink({ basePath: '/secrets', id: 'secret-1', name: 'cloud-init-secret' });

    expect(screen.getByRole('link', { name: 'cloud-init-secret' })).toHaveAttribute(
      'href',
      '/secrets/secret-1',
    );
  });

  it('avoids a duplicate slash and encodes the resource id', () => {
    renderResourceLink({ basePath: '/secrets/', id: 'secret/1', name: 'cloud-init-secret' });

    expect(screen.getByRole('link', { name: 'cloud-init-secret' })).toHaveAttribute(
      'href',
      '/secrets/secret%2F1',
    );
  });
});
