import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ExternalIPPool,
  ExternalIPPoolSchema,
  type ExternalIPsCreateRequest,
  ExternalIPsCreateResponseSchema,
  IPFamily,
  type Project,
  ProjectState,
} from '@osac/types';

import ExternalIpWizardPage from './ExternalIpWizardPage';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const LIST_PATH = '/networking/external-ips';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useBlocker: () => ({ state: 'unblocked' as const }),
  };
});

const makeProject = (id: string, name: string): Project =>
  ({
    id,
    metadata: { name },
    spec: { title: name },
    status: { state: ProjectState.ACTIVE },
  }) as Project;

const makePool = (id: string, name: string): ExternalIPPool =>
  create(ExternalIPPoolSchema, {
    id,
    metadata: { name },
    spec: { ipFamily: IPFamily.IP_FAMILY_IPV4 },
    status: { available: 8n },
  });

const renderCreatePage = (
  overrides?: MockTransportOverrides,
  pools: ExternalIPPool[] = [makePool('pool-1', 'public-edge')],
) =>
  renderWithProviders(<ExternalIpWizardPage />, {
    transportOverrides: overrides,
    apiFixtures: {
      projects: [makeProject('p-1', '')],
      externalIpPools: pools,
    },
  });

const clickNext = async (user: UserEvent) => {
  const [next] = screen.getAllByRole('button', { name: 'Next' });
  await user.click(next);
};

const fillValidWizard = async (user: UserEvent) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'edge-ip');
  const projectToggle = await waitFor(() => {
    const toggle = screen.getByRole('button', {
      name: (_accessibleName, element) => element.id === 'metadata.project',
    });
    expect(toggle).not.toBeDisabled();
    return toggle;
  });
  await user.click(projectToggle);
  await user.click(screen.getByRole('option', { name: 'Default' }));
  expect(projectToggle).toHaveTextContent('Default');
  expect(projectToggle).not.toHaveAttribute('aria-invalid', 'true');
  expect(screen.queryByText('Project is required')).not.toBeInTheDocument();
  await clickNext(user);
  await screen.findByRole('heading', { name: 'Configuration' });
  await waitFor(() => {
    expect(screen.getByLabelText(/^IP pool/)).not.toBeDisabled();
  });
  await clickNext(user);
  await screen.findByRole('heading', { name: 'Review' });
};

describe('ExternalIpWizardPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('uses General, Configuration, and Review steps', async () => {
    renderCreatePage();

    expect(await screen.findByRole('button', { name: 'General' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'General' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Description' })).toBeInTheDocument();
    expect(screen.queryByLabelText(/^IP pool/)).not.toBeInTheDocument();
  });

  it('creates an external IP and navigates to the list', async () => {
    const { user } = renderCreatePage({
      onExternalIpCreate: (req: ExternalIPsCreateRequest) =>
        create(ExternalIPsCreateResponseSchema, {
          object: {
            id: 'eip-1',
            metadata: req.object?.metadata,
            spec: req.object?.spec,
          },
        }),
    });

    await fillValidWizard(user);
    await user.click(screen.getByRole('button', { name: 'Create external IP' }));

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(LIST_PATH);
    });
  });

  it('shows an error when create fails', async () => {
    const { user } = renderCreatePage({
      onExternalIpCreate: () => {
        throw new ConnectError('pool exhausted', Code.ResourceExhausted);
      },
    });

    await fillValidWizard(user);
    await user.click(screen.getByRole('button', { name: 'Create external IP' }));

    expect(await screen.findByText('Failed to create external IP')).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('cancels back to the list without creating', async () => {
    const onExternalIpCreate = vi.fn();
    const { user } = renderCreatePage({ onExternalIpCreate });

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onExternalIpCreate).not.toHaveBeenCalled();
    expect(mockNavigate).toHaveBeenCalledWith(LIST_PATH);
  });
});
