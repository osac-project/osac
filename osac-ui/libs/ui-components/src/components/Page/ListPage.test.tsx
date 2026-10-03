import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ListPage from './ListPage';

describe('ListPage', () => {
  describe('rendering', () => {
    it('renders the title, description, and children', () => {
      render(
        <ListPage title="Items" description="Manage items for your organization.">
          <p>Page body</p>
        </ListPage>,
      );

      expect(screen.getByRole('heading', { name: 'Items', level: 1 })).toBeInTheDocument();
      expect(screen.getByText('Manage items for your organization.')).toBeInTheDocument();
      expect(screen.getByText('Page body')).toBeInTheDocument();
    });

    it('renders a breadcrumb above the title when provided', () => {
      render(
        <ListPage title="Items" breadcrumb={<nav aria-label="Breadcrumb">All items</nav>}>
          <p>Page body</p>
        </ListPage>,
      );

      expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('All items');
    });

    it('renders actions when there is no error', () => {
      render(
        <ListPage title="Items" actions={<button type="button">Create item</button>}>
          <p>Page body</p>
        </ListPage>,
      );

      expect(screen.getByRole('button', { name: 'Create item' })).toBeInTheDocument();
    });

    it('hides actions when an error is present', () => {
      render(
        <ListPage
          title="Items"
          error={new Error('unavailable')}
          actions={<button type="button">Create item</button>}
        >
          <p>Page body</p>
        </ListPage>,
      );

      expect(screen.queryByRole('button', { name: 'Create item' })).not.toBeInTheDocument();
    });
  });
});
