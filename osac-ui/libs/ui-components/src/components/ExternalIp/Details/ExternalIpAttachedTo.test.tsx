import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ExternalIpAttachedTo from './ExternalIpAttachedTo';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ExternalIpAttachedTo', () => {
  it('renders nothing when the IP is not attached', () => {
    renderWithProviders(<ExternalIpAttachedTo />);
    expect(screen.queryByText('No')).not.toBeInTheDocument();
    expect(screen.queryByText('Yes')).not.toBeInTheDocument();
  });

  it('renders nothing when attached without a resolved target', () => {
    renderWithProviders(<ExternalIpAttachedTo attached />);
    expect(screen.queryByText('Yes')).not.toBeInTheDocument();
  });

  it('renders a link to the target followed by its kind', () => {
    renderWithProviders(
      <ExternalIpAttachedTo
        attached
        target={{
          kind: 'baremetalInstance',
          name: 'aaa',
          href: '/bare-metal/bm-1',
        }}
      />,
    );

    expect(screen.getByText('(Bare-metal instance)')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Bare-metal instance: aaa' })).toHaveAttribute(
      'href',
      '/bare-metal/bm-1',
    );
  });
});
