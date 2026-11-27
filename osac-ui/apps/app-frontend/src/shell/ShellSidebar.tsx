import * as React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  Content,
  ContentVariants,
  Divider,
  Nav,
  NavExpandable,
  NavItem,
  NavList,
  PageSidebar,
  PageSidebarBody,
} from '@patternfly/react-core';

import {
  type ServiceWithMethod,
  usePermissions,
  usePermissionsForServices,
} from '@osac/ui-components/hooks/use-permissions';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { type NavLink, type NavSection, isNavSection, navRowsForRole } from './shellNav';

import './ShellSidebar.css';

const ShellNavLink = ({ item }: { item: NavLink }) => {
  const location = useLocation();
  const navigate = useNavigate();

  return (
    <NavItem
      itemId={item.id}
      isActive={location.pathname === item.path || location.pathname.startsWith(item.path + '/')}
      to={item.path}
      onClick={(e) => {
        e.preventDefault();
        navigate(item.path);
      }}
    >
      {item.label}
    </NavItem>
  );
};

const PermissionedShellNavItem = ({
  item,
  resource,
}: {
  item: NavLink;
  resource: ServiceWithMethod<'list'>;
}) => {
  const { allowed, isLoading } = usePermissions(resource, 'list');

  return !isLoading && allowed ? <ShellNavLink item={item} /> : null;
};

const ShellNavItem = ({ item }: { item: NavLink }) =>
  item.resource ? (
    <PermissionedShellNavItem item={item} resource={item.resource} />
  ) : (
    <ShellNavLink item={item} />
  );

const ShellNavSection = ({ section }: { section: NavSection }) => {
  const permissionedChildren = section.children.filter(
    (item): item is NavLink & { resource: ServiceWithMethod<'list'> } =>
      item.resource !== undefined,
  );
  const permissions = usePermissionsForServices(
    permissionedChildren.map((item) => item.resource),
    'list',
  );
  const permissionByItem = new Map(
    permissionedChildren.map((item, index) => [item.id, permissions[index]?.allowed === true]),
  );

  const visibleChildren = section.children.filter(
    (item) => !item.resource || permissionByItem.get(item.id) === true,
  );

  return visibleChildren.length > 0 ? (
    <NavExpandable key={section.id} title={section.label} isExpanded>
      {visibleChildren.map((item) => (
        <ShellNavLink key={item.id} item={item} />
      ))}
    </NavExpandable>
  ) : null;
};

export const ShellSidebar = () => {
  const { role, enabledServices } = useSession();
  const { t } = useTranslation();

  const navRows = React.useMemo(
    () => navRowsForRole(role, t, enabledServices),
    [role, t, enabledServices],
  );

  return (
    <PageSidebar>
      <PageSidebarBody usePageInsets isFilled>
        <Content className="osac-navbar-title" component={ContentVariants.h2}>
          {t('Cloud Extensions')}
        </Content>
        <Divider />
        <Nav aria-label="Primary navigation">
          <NavList>
            {navRows.map((row) => {
              if (isNavSection(row)) {
                return <ShellNavSection key={row.id} section={row} />;
              }
              return <ShellNavItem key={row.id} item={row} />;
            })}
          </NavList>
        </Nav>
      </PageSidebarBody>
    </PageSidebar>
  );
};
