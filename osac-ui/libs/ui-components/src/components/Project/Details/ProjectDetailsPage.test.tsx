import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { type Project, Projects } from '@osac/types';

import ProjectDetailsPage from './ProjectDetailsPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/use-resource', () => ({
  useGetResource: vi.fn(),
  useListResource: vi.fn(),
}));

vi.mock('./ProjectDetailsActionButtons', () => ({
  default: () => null,
}));

vi.mock('../../ProjectMembership/ProjectMembershipTable', () => ({
  default: () => null,
}));

const { useGetResource, useListResource } = await import('@osac/ui-components/api/use-resource');

const makeProject = (id: string, name: string): Project =>
  ({
    id,
    metadata: { name },
    spec: { title: name },
  }) as Project;

const root = makeProject('project-root', '');
const parent = makeProject('project-foo', 'foo');
const project = makeProject('project-foo-bar', 'foo.bar');

describe('ProjectDetailsPage', () => {
  beforeEach(() => {
    vi.mocked(useGetResource).mockReturnValue({
      data: { object: project },
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useGetResource>);
    vi.mocked(useListResource).mockReturnValue({
      data: { items: [root, parent] },
      isLoading: false,
    } as unknown as ReturnType<typeof useListResource>);
  });

  it('loads all parent projects and uses their ids in the breadcrumb', () => {
    renderWithProviders(<ProjectDetailsPage />);

    expect(vi.mocked(useListResource)).toHaveBeenCalledWith(
      Projects,
      { filter: 'this.metadata.tenant != "shared" && this.metadata.name in ["", "foo"]' },
      { enabled: true },
    );
    expect(screen.getByRole('link', { name: 'Root' })).toHaveAttribute(
      'href',
      '/projects/project-root',
    );
    expect(screen.getByRole('link', { name: 'foo' })).toHaveAttribute(
      'href',
      '/projects/project-foo',
    );
    expect(screen.getByRole('heading', { name: 'bar' })).toBeInTheDocument();
  });

  it('replaces loading parent breadcrumbs when the parent list finishes loading', () => {
    vi.mocked(useListResource).mockReturnValueOnce({
      data: undefined,
      isLoading: true,
    } as unknown as ReturnType<typeof useListResource>);

    const { container, rerender } = renderWithProviders(<ProjectDetailsPage />);

    expect(container.querySelectorAll('.pf-v6-c-skeleton')).toHaveLength(2);

    rerender(<ProjectDetailsPage />);

    expect(container.querySelectorAll('.pf-v6-c-skeleton')).toHaveLength(0);
    expect(screen.getByRole('link', { name: 'Root' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'foo' })).toBeInTheDocument();
  });
});
