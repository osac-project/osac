import { Route, Routes } from 'react-router-dom';
import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ComputeInstances } from '@osac/types';

import { VmDetailsPage } from './VmDetailsPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mocks = vi.hoisted(() => {
  const useGetResource = vi.fn();
  return { useGetResource };
});

vi.mock('../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/use-resource')>();
  mocks.useGetResource.mockImplementation(actual.useGetResource);
  return { ...actual, useGetResource: mocks.useGetResource };
});

vi.mock('../Resource/ResourceDetailsPageLoading', () => ({
  ResourceDetailsPageLoading: ({ tabLabels }: { tabLabels?: string[] }) => (
    <div>Loading tabs: {tabLabels?.join(', ')}</div>
  ),
}));

vi.mock('./DetailsPage/VmDetails', () => ({
  default: () => <div>VM details loaded</div>,
}));

const renderPage = () =>
  renderWithProviders(
    <Routes>
      <Route path="/vms/:id" element={<VmDetailsPage />} />
    </Routes>,
    { routerEntries: ['/vms/vm-1'] },
  );

describe('VmDetailsPage', () => {
  beforeEach(async () => {
    const actual =
      await vi.importActual<typeof import('../../api/use-resource')>('../../api/use-resource');
    mocks.useGetResource.mockReset();
    mocks.useGetResource.mockImplementation(actual.useGetResource);
  });

  it('passes shared VM tab labels to the loading skeleton', async () => {
    const actual =
      await vi.importActual<typeof import('../../api/use-resource')>('../../api/use-resource');
    mocks.useGetResource.mockImplementation((...args: Parameters<typeof actual.useGetResource>) => {
      const [service] = args;
      if (service === ComputeInstances) {
        return {
          data: undefined,
          isLoading: true,
          isError: false,
          error: null,
          refetch: vi.fn(),
        } as unknown as ReturnType<typeof actual.useGetResource>;
      }
      return actual.useGetResource(...args);
    });

    renderPage();

    expect(screen.getByText('Loading tabs: Overview, Networking, Console')).toBeInTheDocument();
  });
});
