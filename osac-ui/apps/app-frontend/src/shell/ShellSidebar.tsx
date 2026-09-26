import * as React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  Nav,
  NavExpandable,
  NavItem,
  NavList,
  PageSidebar,
  PageSidebarBody,
} from '@patternfly/react-core';

import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { type NavLink, isNavSection, navRowsForRole } from './shellNav';

const ShellNavItem = ({ item }: { item: NavLink }) => {
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
        <Nav aria-label="Primary navigation">
          <NavList>
            {navRows.map((row) => {
              if (isNavSection(row)) {
                return (
                  <NavExpandable key={row.id} title={row.label}>
                    {row.children.map((item) => (
                      <ShellNavItem key={item.id} item={item} />
                    ))}
                  </NavExpandable>
                );
              }
              return <ShellNavItem key={row.id} item={row} />;
            })}
          </NavList>
        </Nav>
      </PageSidebarBody>
    </PageSidebar>
  );
};
