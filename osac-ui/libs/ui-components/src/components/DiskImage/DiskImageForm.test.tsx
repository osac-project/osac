import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  Architecture,
  DiskImagesCreateResponseSchema,
  GuestOSFamily,
  ServiceTier,
  SourceType,
} from '@osac/types';
import type { Tenant } from '@osac/types/private';

import DiskImageForm, { DISK_IMAGES_LIST_ROUTE } from './DiskImageForm';
import type { UserRole } from '../../shellTypes';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('../../hooks/use-session', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-user',
    username: 'testuser',
    tenantId: 'tenant-1',
    enabledServices: [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
  })),
}));

const { useSession } = await import('../../hooks/use-session');

const makeSession = (role: UserRole) => ({
  role,
  username: 'testuser',
  tenantId: 'tenant-1',
  enabledServices: [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
  userTheme: 'system' as const,
  resolvedTheme: 'light' as const,
  setUserTheme: vi.fn(),
  userContrast: 'system' as const,
  resolvedContrast: 'glass' as const,
  setUserContrast: vi.fn(),
  projects: [],
  setProjects: vi.fn(),
});

const makeTenant = (id: string, name: string): Tenant => ({ id, metadata: { name } }) as Tenant;

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const renderForm = (overrides?: MockTransportOverrides, tenants: Tenant[] = []) =>
  renderWithProviders(<DiskImageForm />, {
    transportOverrides: overrides,
    apiFixtures: { tenants },
  });

const fillValidForm = async (user: ReturnType<typeof renderForm>['user']) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'rhel-9');
  await user.type(
    screen.getByRole('textbox', { name: 'Source reference' }),
    'quay.io/example/rhel:9',
  );
  await user.click(screen.getByRole('button', { name: 'Architecture' }));
  await user.click(screen.getByRole('option', { name: 'amd64' }));
};

describe('DiskImageForm', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders the documented create-mode fields', () => {
    renderForm();

    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Source reference' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Guest OS family' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Architecture' })).toBeInTheDocument();
    expect(screen.getByText('REGISTRY')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('shows a validation error when submitting an empty source reference', async () => {
    const { user } = renderForm();

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Source reference is required')).toBeInTheDocument();
    });
  });

  it('shows a validation error when submitting with no architecture selected', async () => {
    const { user } = renderForm();

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'rhel-9');
    await user.type(
      screen.getByRole('textbox', { name: 'Source reference' }),
      'quay.io/example/rhel:9',
    );
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Select at least one architecture')).toBeInTheDocument();
    });
  });

  it('navigates back to the disk image list on cancel', async () => {
    const { user } = renderForm();

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(mockNavigate).toHaveBeenCalledWith(DISK_IMAGES_LIST_ROUTE);
  });

  describe('submission', () => {
    const CREATED_ID = 'disk-image-created-1';

    it('creates the disk image with the expected payload and navigates to its detail page', async () => {
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm({
        onDiskImageCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(DiskImagesCreateResponseSchema, {
            object: { ...req.object, id: CREATED_ID },
          });
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(`${DISK_IMAGES_LIST_ROUTE}/${CREATED_ID}`);
      });
      const object = captured?.object as {
        metadata?: { name?: string; tenant?: string };
        spec?: { sourceType?: SourceType; sourceRef?: string; architecture?: Architecture[] };
      };
      expect(object?.metadata?.name).toBe('rhel-9');
      expect(object?.metadata?.tenant).toBe('');
      expect(object?.spec?.sourceType).toBe(SourceType.REGISTRY);
      expect(object?.spec?.sourceRef).toBe('quay.io/example/rhel:9');
      expect(object?.spec?.architecture).toEqual([Architecture.AMD64]);
    });

    it('defaults guest OS family to Linux', async () => {
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm({
        onDiskImageCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(DiskImagesCreateResponseSchema, {
            object: { ...req.object, id: CREATED_ID },
          });
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(`${DISK_IMAGES_LIST_ROUTE}/${CREATED_ID}`);
      });
      const spec = (captured?.object as { spec?: { guestOsFamily?: GuestOSFamily } })?.spec;
      expect(spec?.guestOsFamily).toBe(GuestOSFamily.GUEST_OS_FAMILY_LINUX);
    });

    it('maps an InvalidArgument mentioning source_ref onto the source reference field, retaining entered values', async () => {
      const { user } = renderForm({
        onDiskImageCreate: () => {
          throw new ConnectError("field 'source_ref' must not be empty", Code.InvalidArgument);
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText("field 'source_ref' must not be empty")).toBeInTheDocument();
      });
      expect(screen.queryByText('Failed to save disk image')).not.toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('rhel-9');
      expect(screen.getByRole('textbox', { name: 'Source reference' })).toHaveValue(
        'quay.io/example/rhel:9',
      );
    });

    it('maps an InvalidArgument mentioning architecture onto the architecture field', async () => {
      const { user } = renderForm({
        onDiskImageCreate: () => {
          throw new ConnectError(
            "field 'architecture' must contain at least one value",
            Code.InvalidArgument,
          );
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(
          screen.getByText("field 'architecture' must contain at least one value"),
        ).toBeInTheDocument();
      });
      expect(screen.queryByText('Failed to save disk image')).not.toBeInTheDocument();
    });

    it('falls back to the generic alert for an InvalidArgument that names no known field', async () => {
      const { user } = renderForm({
        onDiskImageCreate: () => {
          throw new ConnectError('request rejected', Code.InvalidArgument);
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Failed to save disk image')).toBeInTheDocument();
      });
      expect(screen.getByText('request rejected')).toBeInTheDocument();
    });
  });

  describe('scope control', () => {
    beforeEach(() => {
      vi.mocked(useSession).mockReturnValue(makeSession('tenant-user'));
    });

    it('is absent for a tenant-user session', () => {
      renderForm();

      expect(screen.queryByRole('button', { name: 'Scope' })).not.toBeInTheDocument();
    });

    it('is absent for a tenant-admin session', () => {
      vi.mocked(useSession).mockReturnValue(makeSession('tenant-admin'));
      renderForm();

      expect(screen.queryByRole('button', { name: 'Scope' })).not.toBeInTheDocument();
    });

    it('renders for a provider-admin session, defaulting to Global', () => {
      vi.mocked(useSession).mockReturnValue(makeSession('admin'));
      renderForm(undefined, [makeTenant('tenant-1', 'Tenant One')]);

      expect(screen.getByRole('button', { name: 'Scope' })).toHaveTextContent('Global');
    });

    it('omits metadata.tenant from the create payload for a non-admin session', async () => {
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm({
        onDiskImageCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(DiskImagesCreateResponseSchema, {
            object: { ...req.object, id: 'disk-image-created-1' },
          });
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalled();
      });
      const metadata = (captured?.object as { metadata?: { tenant?: string } })?.metadata;
      expect(metadata?.tenant).toBe('');
    });

    it('includes the selected tenant in the create payload for a provider-admin session', async () => {
      vi.mocked(useSession).mockReturnValue(makeSession('admin'));
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm(
        {
          onDiskImageCreate: (req) => {
            captured = req as unknown as Record<string, unknown>;
            return create(DiskImagesCreateResponseSchema, {
              object: { ...req.object, id: 'disk-image-created-1' },
            });
          },
        },
        [makeTenant('tenant-1', 'Tenant One')],
      );

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Scope' }));
      await user.click(screen.getByRole('option', { name: 'Tenant One' }));
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalled();
      });
      const metadata = (captured?.object as { metadata?: { tenant?: string } })?.metadata;
      expect(metadata?.tenant).toBe('tenant-1');
    });
  });
});
