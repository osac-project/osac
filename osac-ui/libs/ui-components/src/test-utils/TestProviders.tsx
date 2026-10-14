import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider } from 'react-i18next';
import { RouterProvider, createMemoryRouter } from 'react-router-dom';
import type { Transport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type RenderOptions, type RenderResult, render } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';

import {
  type MockApiFixtures,
  type MockTransportOverrides,
  createMockConnectTransport,
} from './createMockConnectTransport';
import en from '../../../i18n/locales/en/translation.json';
import { ApiProvider } from '../api/api-context';
import ToastProvider from '../components/Toast/ToastProvider';

const createTestI18n = () => {
  const instance = i18n.createInstance();
  instance.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: en } },
    interpolation: { escapeValue: false },
  });
  return instance;
};

export type TestProvidersProps = {
  children: ReactNode;
  apiFixtures?: MockApiFixtures;
  transport?: Transport;
  transportOverrides?: MockTransportOverrides;
  routerEntries?: string[];
};

export const TestProviders = ({
  children,
  apiFixtures,
  transport: transportOverride,
  transportOverrides,
  routerEntries,
}: TestProvidersProps) => {
  const i18nInstance = createTestI18n();
  const transport =
    transportOverride ?? createMockConnectTransport(apiFixtures, transportOverrides);
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, refetchInterval: false },
    },
  });
  // A data router (rather than a plain <MemoryRouter>) is required so that
  // data-router-only hooks like useBlocker (used by LeaveFormConfirmation) work in tests.
  const router = createMemoryRouter([{ path: '*', element: children }], {
    initialEntries: routerEntries,
  });

  return (
    <I18nextProvider i18n={i18nInstance}>
      <ApiProvider transport={transport}>
        <QueryClientProvider client={queryClient}>
          <ToastProvider>
            <RouterProvider router={router} />
          </ToastProvider>
        </QueryClientProvider>
      </ApiProvider>
    </I18nextProvider>
  );
};

export type RenderWithProvidersOptions = Omit<RenderOptions, 'wrapper'> & {
  apiFixtures?: MockApiFixtures;
  transport?: Transport;
  transportOverrides?: MockTransportOverrides;
  routerEntries?: string[];
};

export type RenderWithProvidersResult = RenderResult & {
  user: UserEvent;
};

export const renderWithProviders = (
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult => {
  const { apiFixtures, transport, transportOverrides, routerEntries, ...renderOptions } = options;

  const view = render(ui, {
    wrapper: ({ children }) => (
      <TestProviders
        apiFixtures={apiFixtures}
        transport={transport}
        transportOverrides={transportOverrides}
        routerEntries={routerEntries}
      >
        {children}
      </TestProviders>
    ),
    ...renderOptions,
  });

  return { ...view, user: userEvent.setup() };
};
