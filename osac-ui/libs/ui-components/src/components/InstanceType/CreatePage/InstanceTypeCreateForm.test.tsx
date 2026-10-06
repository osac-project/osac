import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { InstanceTypesCreateResponseSchema } from '@osac/types/private';

import InstanceTypeCreateForm from './InstanceTypeCreateForm';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const LIST_ROUTE = '/admin/infrastructure/instance-types';

const renderForm = (overrides?: MockTransportOverrides) =>
  renderWithProviders(<InstanceTypeCreateForm />, {
    transportOverrides: overrides,
  });

const getNumericInput = (name: string) => screen.getByRole('spinbutton', { name });

const fillValidForm = async (user: ReturnType<typeof renderForm>['user']) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'gp-small');
  fireEvent.change(getNumericInput('vCPUs'), {
    target: { value: '4' },
  });
  fireEvent.change(getNumericInput('Memory (GiB)'), {
    target: { value: '16' },
  });
};

describe('InstanceTypeCreateForm', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders the documented fields', () => {
    renderForm();

    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Description' })).toBeInTheDocument();
    expect(getNumericInput('vCPUs')).toHaveAttribute('type', 'number');
    expect(getNumericInput('Memory (GiB)')).toHaveAttribute('type', 'number');
    expect(screen.getByText('GPU')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'PCI device selector' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Resource name' })).toBeInTheDocument();
    expect(getNumericInput('GPU count')).toHaveAttribute('type', 'number');
    expect(screen.getAllByRole('button', { name: 'Minus' })).toHaveLength(3);
    expect(screen.getAllByRole('button', { name: 'Plus' })).toHaveLength(3);
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('keeps the Create button enabled before any interaction', () => {
    renderForm();

    expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled();
  });

  it('shows a validation error when submitting an empty name, without disabling Create', async () => {
    const { user } = renderForm();

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled();
  });

  it.each([
    ['blank', '', 'vCPUs are required'],
    ['decimal', '1.5', 'Must be a whole number'],
    ['zero', '0', 'Must be greater than zero'],
    ['negative', '-1', 'Must be greater than zero'],
  ])('shows a validation error for %s vcpus', async (_label, vcpus, expectedMessage) => {
    const { user } = renderForm();

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'gp-small');
    fireEvent.change(getNumericInput('vCPUs'), {
      target: { value: vcpus },
    });
    fireEvent.change(getNumericInput('Memory (GiB)'), {
      target: { value: '16' },
    });
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText(expectedMessage)).toBeInTheDocument();
    });
  });

  it.each([
    ['blank', '', 'Memory is required'],
    ['decimal', '1.5', 'Must be a whole number'],
    ['zero', '0', 'Must be greater than zero'],
    ['negative', '-1', 'Must be greater than zero'],
  ])('shows a validation error for %s Memory (GiB)', async (_label, memoryGib, expectedMessage) => {
    const { user } = renderForm();

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'gp-small');
    fireEvent.change(getNumericInput('vCPUs'), {
      target: { value: '4' },
    });
    fireEvent.change(getNumericInput('Memory (GiB)'), {
      target: { value: memoryGib },
    });
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText(expectedMessage)).toBeInTheDocument();
    });
  });

  it.each([
    ['blank', '', 'Required when configuring a GPU'],
    ['decimal', '1.5', 'Must be a whole number'],
    ['zero', '0', 'Must be greater than zero'],
    ['negative', '-1', 'Must be greater than zero'],
  ])('shows a validation error for %s GPU count', async (_label, count, expectedMessage) => {
    const { user } = renderForm();

    await fillValidForm(user);
    await user.type(screen.getByRole('textbox', { name: 'PCI device selector' }), '10DE:20B0');
    await user.type(screen.getByRole('textbox', { name: 'Resource name' }), 'nvidia.com/A100');
    fireEvent.change(getNumericInput('GPU count'), {
      target: { value: count },
    });
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText(expectedMessage)).toBeInTheDocument();
    });
  });

  it.each(['vCPUs', 'Memory (GiB)', 'GPU count'])(
    'ignores non-numeric input for %s',
    async (fieldLabel) => {
      const { user } = renderForm();

      const input = getNumericInput(fieldLabel);
      await user.type(input, 'test');

      expect(input).toHaveValue(null);
    },
  );

  it.each([
    ['GPU count', 'PCI device selector', '10DE:20B0'],
    ['Resource name', 'PCI device selector', '10DE:20B0'],
    ['PCI device selector', 'Resource name', 'nvidia.com/A100'],
  ])(
    'shows required validation for touched blank GPU field %s',
    async (touchedField, configuredField, configuredValue) => {
      const { user } = renderForm();

      await user.type(screen.getByRole('textbox', { name: configuredField }), configuredValue);
      const touchedInput =
        touchedField === 'GPU count'
          ? getNumericInput(touchedField)
          : screen.getByRole('textbox', { name: touchedField });
      await user.click(touchedInput);
      await user.tab();

      expect(await screen.findByText('Required when configuring a GPU')).toBeInTheDocument();
    },
  );

  it('navigates back to the instance type list on cancel', async () => {
    const { user } = renderForm();

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(mockNavigate).toHaveBeenCalledWith(LIST_ROUTE);
  });

  describe('submission', () => {
    const CREATED_ID = 'instance-type-created-1';

    it('creates the instance type with integer-converted vcpus/memory and navigates to its detail page', async () => {
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm({
        onInstanceTypeCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(InstanceTypesCreateResponseSchema, {
            object: { ...req.object, id: CREATED_ID },
          });
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(`${LIST_ROUTE}/${CREATED_ID}`);
      });
      const spec = (captured?.object as { spec?: { vcpus?: number; memoryGib?: number } })?.spec;
      expect(spec?.vcpus).toBe(4);
      expect(spec?.memoryGib).toBe(16);
    });

    it('omits gpu from the request when the GPU fields are left blank', async () => {
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm({
        onInstanceTypeCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(InstanceTypesCreateResponseSchema, {
            object: { ...req.object, id: CREATED_ID },
          });
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(`${LIST_ROUTE}/${CREATED_ID}`);
      });
      const spec = captured?.object as { spec?: { gpu?: unknown } };
      expect(spec?.spec?.gpu).toBeUndefined();
    });

    it('includes gpu in the request when all three GPU fields are filled in', async () => {
      let captured: Record<string, unknown> | undefined;
      const { user } = renderForm({
        onInstanceTypeCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(InstanceTypesCreateResponseSchema, {
            object: { ...req.object, id: CREATED_ID },
          });
        },
      });

      await fillValidForm(user);
      await user.type(screen.getByRole('textbox', { name: 'PCI device selector' }), '10DE:20B0');
      await user.type(screen.getByRole('textbox', { name: 'Resource name' }), 'nvidia.com/A100');
      fireEvent.change(getNumericInput('GPU count'), {
        target: { value: '2' },
      });
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(`${LIST_ROUTE}/${CREATED_ID}`);
      });
      const spec = (
        captured?.object as {
          spec?: { gpu?: { pciDeviceSelector?: string; resourceName?: string; count?: number } };
        }
      )?.spec;
      expect(spec?.gpu).toMatchObject({
        pciDeviceSelector: '10DE:20B0',
        resourceName: 'nvidia.com/A100',
        count: 2,
      });
    });

    it('shows a validation error when only some GPU fields are filled in', async () => {
      const { user } = renderForm();

      await fillValidForm(user);
      await user.type(screen.getByRole('textbox', { name: 'PCI device selector' }), '10DE:20B0');
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getAllByText('Required when configuring a GPU').length).toBeGreaterThan(0);
      });
    });

    it('shows an error alert when create fails, regardless of the backend message shape', async () => {
      const { user } = renderForm({
        onInstanceTypeCreate: () => {
          throw new ConnectError(
            "field 'spec.vcpus' must be greater than zero",
            Code.InvalidArgument,
          );
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Failed to create instance type')).toBeInTheDocument();
      });
      expect(screen.getByText("field 'spec.vcpus' must be greater than zero")).toBeInTheDocument();
    });
  });
});
