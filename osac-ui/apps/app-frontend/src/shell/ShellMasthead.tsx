import * as React from 'react';
import { useNavigate } from 'react-router-dom';
import { ReactSVG } from 'react-svg';
import {
  Alert,
  Button,
  Dropdown,
  DropdownItem,
  DropdownList,
  Label,
  Masthead,
  MastheadBrand,
  MastheadContent,
  MastheadLogo,
  MastheadMain,
  MastheadToggle,
  MenuToggle,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageToggleButton,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';
import { UserIcon } from '@patternfly/react-icons/dist/esm/icons/user-icon';

import redHatLogoIcon from '@osac/ui-components/assets/Logo-Red_Hat-A-Standard-RGB.svg';
import { SubtleContent } from '@osac/ui-components/components/SubtleContent/SubtleContent';
import UserPreferencesModal from '@osac/ui-components/components/UserPreferences/UserPreferencesModal';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { userRoleLabels } from '@osac/ui-components/shellTypes';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import './ShellMasthead.css';

interface ShellMastheadProps {
  onLogout: () => Promise<void>;
}

export const ShellMasthead = ({ onLogout }: ShellMastheadProps) => {
  const { t } = useTranslation();
  const [isUserMenuOpen, setIsUserMenuOpen] = React.useState(false);
  const [isPreferencesOpen, setPreferencesOpen] = React.useState(false);
  const [logoutError, setLogoutError] = React.useState<string>();
  const navigate = useNavigate();
  const { role, username, tenantId } = useSession();
  const displayName = username || t('User');

  return (
    <>
      {logoutError && (
        <Modal variant="small" isOpen onClose={() => setLogoutError(undefined)}>
          <ModalHeader title={t('Logout failed')} titleIconVariant="danger" />
          <ModalBody>
            <Alert variant="danger" isInline title={logoutError ?? ''} />
          </ModalBody>
          <ModalFooter>
            <Button variant="primary" onClick={() => setLogoutError(undefined)}>
              {t('Close')}
            </Button>
          </ModalFooter>
        </Modal>
      )}
      {isPreferencesOpen && <UserPreferencesModal onClose={() => setPreferencesOpen(false)} />}
      <Masthead display={{ default: 'inline' }}>
        <MastheadMain>
          <MastheadToggle>
            <PageToggleButton isHamburgerButton aria-label={t('Global navigation')} />
          </MastheadToggle>
          <MastheadBrand className="osac-masthead-brand">
            <MastheadLogo
              aria-label={t('Red Hat')}
              component={(props) => <a {...props} href="#" />}
            >
              <ReactSVG src={redHatLogoIcon} aria-hidden className="pf-v6-c-brand" />
            </MastheadLogo>
            {tenantId ? (
              <SubtleContent>{t('Tenant: {{ tenantId }}', { tenantId })}</SubtleContent>
            ) : null}
          </MastheadBrand>
        </MastheadMain>

        <MastheadContent>
          <Toolbar id="toolbar" isStatic>
            <ToolbarContent>
              <ToolbarGroup
                variant="action-group-plain"
                align={{ default: 'alignEnd' }}
                gap={{ default: 'gapNone', md: 'gapMd' }}
              >
                <ToolbarItem>
                  <Dropdown
                    isOpen={isUserMenuOpen}
                    onSelect={() => setIsUserMenuOpen(false)}
                    onOpenChange={setIsUserMenuOpen}
                    popperProps={{ position: 'right' }}
                    toggle={(ref) => (
                      <MenuToggle
                        ref={ref}
                        isExpanded={isUserMenuOpen}
                        onClick={() => setIsUserMenuOpen(!isUserMenuOpen)}
                        icon={<UserIcon />}
                        aria-label={t('Account menu')}
                      >
                        {displayName}{' '}
                        <Label color="grey" variant="outline" isCompact>
                          {userRoleLabels(t)[role]}
                        </Label>
                      </MenuToggle>
                    )}
                  >
                    <DropdownList>
                      <DropdownItem onClick={() => setPreferencesOpen(true)}>
                        {t('Preferences')}
                      </DropdownItem>
                      <DropdownItem
                        onClick={async () => {
                          try {
                            await onLogout();
                            navigate('/');
                          } catch (e) {
                            setLogoutError(getErrorMessage(e));
                          }
                        }}
                      >
                        {t('Log out')}
                      </DropdownItem>
                    </DropdownList>
                  </Dropdown>
                </ToolbarItem>
              </ToolbarGroup>
            </ToolbarContent>
          </Toolbar>
        </MastheadContent>
      </Masthead>
    </>
  );
};
