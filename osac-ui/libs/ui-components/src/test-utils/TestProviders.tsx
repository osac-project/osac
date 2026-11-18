import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider } from 'react-i18next';
import { RouterProvider, createMemoryRouter } from 'react-router-dom';
import type { Transport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type RenderOptions, type RenderResult, render } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';

import { Capabilities, ServiceTier } from '@osac/types';

import {
  type MockApiFixtures,
  type MockTransportOverrides,
  createMockConnectTransport,
} from './createMockConnectTransport';
import en from '../../../i18n/locales/en/translation.json';
import { ApiProvider } from '../api/api-context';
import ToastProvider from '../components/Toast/ToastProvider';
import { SessionProvider } from '../hooks/use-session';
import type { UserRole } from '../shellTypes';

const createTestI18n = () => {
  const instance = i18n.createInstance();
  instance.init({
    initImmediate: false,
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: en } },
    interpolation: { escapeValue: false },
  });
  return instance;
};

type TestSession = {
  role?: UserRole;
  username?: string;
  tenantId?: string;
};

export type TestProvidersProps = {
  children: ReactNode;
  apiFixtures?: MockApiFixtures;
  transport?: Transport;
  transportOverrides?: MockTransportOverrides;
  routerEntries?: string[];
  session?: TestSession;
};

export const TestProviders = ({
  children,
  apiFixtures,
  transport: transportOverride,
  transportOverrides,
  routerEntries,
  session = {},
}: TestProvidersProps) => {
  const i18nInstance = createTestI18n();
  const transport =
    transportOverride ?? createMockConnectTransport(apiFixtures, transportOverrides);
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, refetchInterval: false },
    },
  });
  // Keep the session's capabilities ready on the first render.
  queryClient.setQueryData([Capabilities.typeName, 'get', {}], {
    enabledServices: apiFixtures?.enabledServices ?? [
      ServiceTier.CAAS,
      ServiceTier.VMAAS,
      ServiceTier.BMAAS,
    ],
  });
  // A data router (rather than a plain <MemoryRouter>) is required so that
  // data-router-only hooks like useBlocker (used by LeaveFormConfirmation) work in tests.
  const router = createMemoryRouter(
    [
      {
        path: '*',
        element: (
          <SessionProvider
            role={session.role ?? 'admin'}
            username={session.username ?? 'test-user'}
            tenantId={session.tenantId ?? 'test-tenant'}
          >
            {children}
          </SessionProvider>
        ),
      },
    ],
    { initialEntries: routerEntries },
  );

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
  session?: TestSession;
};

export type RenderWithProvidersResult = RenderResult & {
  user: UserEvent;
};

export const renderWithProviders = (
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult => {
  const { apiFixtures, transport, transportOverrides, routerEntries, session, ...renderOptions } =
    options;

  const view = render(ui, {
    wrapper: ({ children }) => (
      <TestProviders
        apiFixtures={apiFixtures}
        transport={transport}
        transportOverrides={transportOverrides}
        routerEntries={routerEntries}
        session={session}
      >
        {children}
      </TestProviders>
    ),
    ...renderOptions,
  });

  return { ...view, user: userEvent.setup() };
};
