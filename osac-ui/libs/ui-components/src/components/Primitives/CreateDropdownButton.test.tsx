import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import CreateDropdownButton from './CreateDropdownButton';
import { renderWithProviders } from '../../test-utils/TestProviders';

const items = [{ to: '/projects/create', title: 'Project' }];

describe('CreateDropdownButton', () => {
  it('renders a primary create toggle with a plus icon', () => {
    renderWithProviders(<CreateDropdownButton items={items}>Create</CreateDropdownButton>);

    const toggle = screen.getByRole('button', { name: 'Create' });
    expect(toggle).toHaveClass('pf-m-primary');
    expect(toggle.querySelector('.pf-v6-c-menu-toggle__icon')).not.toBeNull();
  });

  it('opens the dropdown and closes it after selecting an item', async () => {
    const { user } = renderWithProviders(
      <CreateDropdownButton items={items}>Create</CreateDropdownButton>,
    );

    await user.click(screen.getByRole('button', { name: 'Create' }));
    const item = screen.getByRole('menuitem', { name: 'Project' });
    expect(item).toHaveAttribute('href', '/projects/create');

    await user.click(item);

    expect(screen.queryByRole('menuitem', { name: 'Project' })).not.toBeInTheDocument();
  });
});
