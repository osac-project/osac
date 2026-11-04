import { Navigate, Route, Routes } from 'react-router-dom';
import {
  Bullseye,
  Button,
  EmptyState,
  EmptyStateActions,
  EmptyStateBody,
  EmptyStateFooter,
  Spinner,
} from '@patternfly/react-core';

import VmConsolePage from '@osac/ui-components/components/vm/VmConsolePage';
import { SessionProvider, useSession } from '@osac/ui-components/hooks/use-session';

import { useOIDCLogin } from './hooks/oidc-login';
import { AppShell } from './shell/AppShell';
import { defaultRouteForRole } from './shell/shellRoutes';

const LoggedInHomeRedirect = () => {
  const { role } = useSession();
  return <Navigate to={defaultRouteForRole(role)} replace />;
};

const App = () => {
  const [username, role, tenantId, isLoading, error, logout] = useOIDCLogin();

  if (isLoading) {
    return (
      <Bullseye>
        <Spinner size="xl" />
      </Bullseye>
    );
  }

  if (error) {
    return (
      <Bullseye>
        <EmptyState titleText="Sign-in failed" headingLevel="h4">
          <EmptyStateBody>{error}</EmptyStateBody>
          <EmptyStateFooter>
            <EmptyStateActions>
              <Button variant="primary">Retry</Button>
            </EmptyStateActions>
          </EmptyStateFooter>
        </EmptyState>
      </Bullseye>
    );
  }

  return (
    <SessionProvider role={role} username={username} tenantId={tenantId}>
      <Routes>
        <Route path="/" element={<LoggedInHomeRedirect />} />

        {role !== 'admin' && <Route path="/vms/:id/console" element={<VmConsolePage />} />}

        <Route path="/*" element={<AppShell logout={logout} />} />
      </Routes>
    </SessionProvider>
  );
};

export default App;
