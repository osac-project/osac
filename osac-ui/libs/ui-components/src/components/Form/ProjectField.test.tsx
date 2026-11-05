import { useState } from 'react';
import { createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import { type Project, Projects } from '@osac/types';

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
  it('renders project options and stores the selected full project path', async () => {
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
    expect(screen.getByRole('option', { name: 'bar' })).toBeInTheDocument();
    expect(screen.queryByText('All projects')).not.toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: 'bar' }));

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

  it("does not offer the previous tenant's projects while the new tenant's list loads", async () => {
    let releaseTenantTwo = () => undefined as void;
    const tenantTwoReady = new Promise<void>((resolve) => {
      releaseTenantTwo = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(Projects, {
        list: async (req) => {
          if (req.filter?.includes('"tenant-2"')) {
            await tenantTwoReady;
            return { items: [makeProject('project-two', 'team-two')], size: 1, total: 1 };
          }
          return { items: [makeProject('project-one', 'team-one')], size: 1, total: 1 };
        },
      });
    });
    const Harness = () => {
      const [tenant, setTenant] = useState('tenant-1');
      return (
        <Formik initialValues={{ metadata: { project: '' } }} onSubmit={() => undefined}>
          <>
            <ProjectField tenant={tenant} />
            <button type="button" onClick={() => setTenant('tenant-2')}>
              switch tenant
            </button>
          </>
        </Formik>
      );
    };
    const { user } = renderWithProviders(<Harness />, { transport });

    await user.click(await screen.findByRole('button', { name: 'Project' }));
    expect(await screen.findByRole('option', { name: 'team-one' })).toBeInTheDocument();
    await user.keyboard('{Escape}');

    await user.click(screen.getByRole('button', { name: 'switch tenant' }));
    expect(await screen.findByText('Loading projects...')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Project' }));
    expect(screen.queryByRole('option', { name: 'team-one' })).not.toBeInTheDocument();
    await user.keyboard('{Escape}');

    releaseTenantTwo();
    await waitFor(() => {
      expect(screen.queryByText('Loading projects...')).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Project' }));
    expect(await screen.findByRole('option', { name: 'team-two' })).toBeInTheDocument();
  });
});
