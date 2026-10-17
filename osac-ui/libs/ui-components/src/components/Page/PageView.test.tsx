import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import PageView from './PageView';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('PageView', () => {
  it('renders the toolbar and list section when data is available', () => {
    renderWithProviders(
      <PageView
        title="Items"
        toolbar={<div>Toolbar</div>}
        showEmptyState={false}
        emptyState={<div>Empty</div>}
        listSection={<div>List content</div>}
      />,
    );

    expect(screen.getByRole('heading', { name: 'Items', level: 1 })).toBeInTheDocument();
    expect(screen.getByText('Toolbar')).toBeInTheDocument();
    expect(screen.getByText('List content')).toBeInTheDocument();
    expect(screen.queryByText('Empty')).not.toBeInTheDocument();
  });

  it('renders the empty state when there is nothing to show', () => {
    renderWithProviders(
      <PageView
        title="Items"
        toolbar={<div>Toolbar</div>}
        showEmptyState
        emptyState={<div>Empty</div>}
        listSection={<div>List content</div>}
      />,
    );

    expect(screen.getByText('Empty')).toBeInTheDocument();
    expect(screen.queryByText('List content')).not.toBeInTheDocument();
  });

  it('renders filtered summary text and clear action when filters are active', () => {
    const onClearAllFilters = vi.fn();

    renderWithProviders(
      <PageView
        title="Items"
        toolbar={<div>Toolbar</div>}
        showEmptyState={false}
        emptyState={<div>Empty</div>}
        listSection={<div>List content</div>}
        filteredText="2 of 5 items"
        isFiltered
        onClearAllFilters={onClearAllFilters}
      />,
    );

    expect(screen.getByText('2 of 5 items')).toBeInTheDocument();
    screen.getByRole('button', { name: 'Clear all filters' }).click();
    expect(onClearAllFilters).toHaveBeenCalledOnce();
  });
});
