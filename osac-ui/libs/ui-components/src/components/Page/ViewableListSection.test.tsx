import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import { getViewTypePrefKey } from '@osac/ui-components/components/Primitives/ViewSwitcher';

import { ViewableListSection } from './ViewableListSection';
import { renderWithProviders } from '../../test-utils/TestProviders';

type TestItem = { id: string; name: string };

const testPageKey = 'test-page';
const testPageViewPrefKey = getViewTypePrefKey(testPageKey);

describe('ViewableListSection', () => {
  const items: TestItem[] = [
    { id: 'one', name: 'First' },
    { id: 'two', name: 'Second' },
  ];

  beforeEach(() => {
    localStorage.clear();
  });

  it('renders cards in card view', () => {
    renderWithProviders(
      <ViewableListSection
        items={items}
        pageKey={testPageKey}
        getItemKey={(item) => item.id}
        renderCard={(item) => <div>{item.name} card</div>}
        renderList={() => <table aria-label="List view" />}
      />,
    );

    expect(screen.getByText('First card')).toBeInTheDocument();
    expect(screen.getByText('Second card')).toBeInTheDocument();
    expect(screen.queryByRole('table', { name: 'List view' })).not.toBeInTheDocument();
  });

  it('renders the list view in list mode', () => {
    localStorage.setItem(testPageViewPrefKey, 'list');

    renderWithProviders(
      <ViewableListSection
        items={items}
        pageKey={testPageKey}
        getItemKey={(item) => item.id}
        renderCard={(item) => <div>{item.name} card</div>}
        renderList={(listItems) => (
          <table aria-label="List view">
            <tbody>
              {listItems.map((item) => (
                <tr key={item.id}>
                  <td>{item.name}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      />,
    );

    expect(screen.getByRole('table', { name: 'List view' })).toBeInTheDocument();
    expect(screen.getByText('First')).toBeInTheDocument();
    expect(screen.queryByText('First card')).not.toBeInTheDocument();
  });

  it('returns nothing when there are no items to show', () => {
    renderWithProviders(
      <ViewableListSection<TestItem>
        items={[]}
        pageKey={testPageKey}
        getItemKey={(item) => item.id}
        renderCard={(item) => <div>{item.name}</div>}
        renderList={() => <table aria-label="List view" />}
      />,
    );

    expect(screen.queryByText('First')).not.toBeInTheDocument();
    expect(screen.queryByRole('grid')).not.toBeInTheDocument();
  });
});
