import { type ReactNode } from 'react';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { Page, SkipToContent } from '@patternfly/react-core';

import { CatalogItemDetailPage } from '@osac/ui-components/components/catalog/details/CatalogItemDetailPage.tsx';
import ErrorBoundary from '@osac/ui-components/components/ErrorBoundary/ErrorBoundary';
import ExternalIpRoutes from '@osac/ui-components/components/ExternalIp/ExternalIpRoutes';
import { ExternalIpPoolRoutes } from '@osac/ui-components/components/ExternalIpPool/ExternalIpPoolRoutes';
import IdentityProviderRoutes from '@osac/ui-components/components/IdentityProvider/IdentityProviderRoutes';
import ProjectRoutes from '@osac/ui-components/components/Project/ProjectRoutes';
import ProjectMembershipRoutes from '@osac/ui-components/components/ProjectMembership/ProjectMembershipRoutes';
import RoleBindingRoutes from '@osac/ui-components/components/RoleBinding/RoleBindingRoutes';
import SecretRoutes from '@osac/ui-components/components/Secret/SecretRoutes';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import AttachNatGatewayWizardPage from '@osac/ui-components/pages/networking/AttachNatGatewayWizardPage';
import { SecurityGroupDetailPage } from '@osac/ui-components/pages/networking/SecurityGroupDetailPage';
import { SecurityGroupsListPage } from '@osac/ui-components/pages/networking/SecurityGroupsListPage';
import { VirtualNetworkDetailPage } from '@osac/ui-components/pages/networking/VirtualNetworkDetailPage';
import { VirtualNetworksListPage } from '@osac/ui-components/pages/networking/VirtualNetworksListPage';
import { BareMetalRoutes } from '@osac/ui-components/pages/tenant/BareMetalRoutes';
import CatalogPage from '@osac/ui-components/pages/tenant/CatalogPage';
import { ClusterRoutes } from '@osac/ui-components/pages/tenant/ClusterRoutes';
import { VmRoutes } from '@osac/ui-components/pages/tenant/VmRoutes';

import { BareMetalInstanceTypeRoutes } from './BareMetalInstanceTypeRoutes';
import { DiskImageRoutes } from './DiskImageRoutes';
import { InstanceTypeRoutes } from './InstanceTypeRoutes';
import { ShellMasthead } from './ShellMasthead';
import { defaultRouteForRole } from './shellRoutes';
import { ShellSidebar } from './ShellSidebar';
import { StorageRoutes } from './StorageRoutes';
import { TenantRoutes } from './TenantRoutes';

const MAIN_CONTENT_ID = 'osac-main-content';

const ShellRoute = ({ children }: { children: ReactNode }) => {
  const { pathname } = useLocation();

  return <ErrorBoundary key={pathname}>{children}</ErrorBoundary>;
};

export const AppShell = ({ logout }: { logout: () => Promise<void> }) => {
  const { role } = useSession();
  const { t } = useTranslation();

  const defaultRoute = defaultRouteForRole(role);

  return (
    <Page
      masthead={<ShellMasthead onLogout={logout} />}
      sidebar={<ShellSidebar />}
      isManagedSidebar
      mainContainerId={MAIN_CONTENT_ID}
      skipToContent={
        <SkipToContent href={`#${MAIN_CONTENT_ID}`}>{t('Skip to content')}</SkipToContent>
      }
    >
      <Routes>
        <Route
          path="/catalog"
          element={
            <ShellRoute>
              <CatalogPage />
            </ShellRoute>
          }
        />
        <Route
          path="/catalog/:kind/:id"
          element={
            <ShellRoute>
              <CatalogItemDetailPage />
            </ShellRoute>
          }
        />
        <Route
          path="/projects/*"
          element={
            <ShellRoute>
              <ProjectRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/project-membership/*"
          element={
            <ShellRoute>
              <ProjectMembershipRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/admin/tenants/*"
          element={
            <ShellRoute>
              <TenantRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/admin/infrastructure/storage/*"
          element={
            <ShellRoute>
              <StorageRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/admin/infrastructure/instance-types/*"
          element={
            <ShellRoute>
              <InstanceTypeRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/admin/infrastructure/baremetal-instance-types/*"
          element={
            <ShellRoute>
              <BareMetalInstanceTypeRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/admin/infrastructure/disk-images/*"
          element={
            <ShellRoute>
              <DiskImageRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/admin/infrastructure/external-ip-pools/*"
          element={
            <ShellRoute>
              <ExternalIpPoolRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/tenant/identity-provider/*"
          element={
            <ShellRoute>
              <IdentityProviderRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/tenant/role-binding/*"
          element={
            <ShellRoute>
              <RoleBindingRoutes />
            </ShellRoute>
          }
        />
        {role !== 'admin' && (
          <>
            <Route
              path="/vms/*"
              element={
                <ShellRoute>
                  <VmRoutes />
                </ShellRoute>
              }
            />
            <Route
              path="/clusters/*"
              element={
                <ShellRoute>
                  <ClusterRoutes />
                </ShellRoute>
              }
            />
            <Route
              path="/bare-metal/*"
              element={
                <ShellRoute>
                  <BareMetalRoutes />
                </ShellRoute>
              }
            />
          </>
        )}
        <Route
          path="/networking/virtual-networks"
          element={
            <ShellRoute>
              <VirtualNetworksListPage />
            </ShellRoute>
          }
        />
        <Route
          path="/networking/virtual-networks/:id/nat-gateway/attach"
          element={
            <ShellRoute>
              <AttachNatGatewayWizardPage />
            </ShellRoute>
          }
        />
        <Route
          path="/networking/virtual-networks/:id"
          element={
            <ShellRoute>
              <VirtualNetworkDetailPage />
            </ShellRoute>
          }
        />
        <Route
          path="/networking/security-groups"
          element={
            <ShellRoute>
              <SecurityGroupsListPage />
            </ShellRoute>
          }
        />
        <Route
          path="/networking/security-groups/:id"
          element={
            <ShellRoute>
              <SecurityGroupDetailPage />
            </ShellRoute>
          }
        />
        <Route
          path="/networking/external-ips/*"
          element={
            <ShellRoute>
              <ExternalIpRoutes />
            </ShellRoute>
          }
        />
        <Route
          path="/secrets/*"
          element={
            <ShellRoute>
              <SecretRoutes />
            </ShellRoute>
          }
        />
        <Route path="*" element={<Navigate to={defaultRoute} replace />} />
      </Routes>
    </Page>
  );
};
