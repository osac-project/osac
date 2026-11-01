import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import { type Project } from '@osac/types';

import ProjectField from './ProjectField';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeProject = (id: string, name: string, parent = ''): Project =>
  ({
    id,
    metadata: { name, project: parent },
    spec: { title: name },
  }) as Project;

const projects = [
  makeProject('project-root', ''),
  makeProject('project-foo', 'foo'),
  makeProject('project-foo-bar', 'foo.bar', 'foo'),
];

describe('ProjectField', () => {
  it('renders a tree and stores the selected full project path', async () => {
    const onSelect = vi.fn();
    const { user } = renderWithProviders(
      <Formik initialValues={{ metadata: { project: '' } }} onSubmit={() => undefined}>
        {({ values }) => (
          <>
            <ProjectField onSelect={onSelect} />
            <output aria-label="formik-value">{values.metadata.project}</output>
          </>
        )}
      </Formik>,
      { apiFixtures: { projects } },
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Project' })).toHaveTextContent('Root');
    });

    await user.click(screen.getByRole('button', { name: 'Project' }));

    expect(screen.getByRole('option', { name: 'Root' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'foo' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /bar Nested/ })).toBeInTheDocument();
    expect(screen.queryByText('All projects')).not.toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /bar Nested/ }));

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent('foo.bar');
    });
    expect(screen.getByRole('button', { name: 'Project' })).toHaveTextContent('bar');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('foo.bar');
  });

  it('keeps Root selected when the selected option is chosen again', async () => {
    const onSelect = vi.fn();
    const { user } = renderWithProviders(
      <Formik initialValues={{ metadata: { project: '' } }} onSubmit={() => undefined}>
        {({ values }) => (
          <>
            <ProjectField onSelect={onSelect} />
            <output aria-label="formik-value">{JSON.stringify(values.metadata.project)}</output>
          </>
        )}
      </Formik>,
      { apiFixtures: { projects } },
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Project' })).toHaveTextContent('Root');
    });
    await user.click(screen.getByRole('button', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: 'Root' }));

    expect(screen.getByLabelText('formik-value')).toHaveTextContent('""');
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('');
    await waitFor(() => {
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    });
  });
});
