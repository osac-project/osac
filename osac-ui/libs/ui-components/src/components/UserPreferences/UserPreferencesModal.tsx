import * as React from 'react';
import { ReactNode } from 'react';
import {
  FormGroup,
  Modal,
  ModalBody,
  ModalHeader,
  ToggleGroup,
  ToggleGroupItem,
} from '@patternfly/react-core';
import { RhUiDarkModeFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-dark-mode-fill-icon';
import { RhUiDesktopIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-desktop-icon';
import { RhUiLightModeFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-light-mode-fill-icon';

import { useSession } from '../../hooks/use-session';
import { Contrast, Theme } from '../../hooks/use-theme';
import { useTranslation } from '../../hooks/useTranslation';
import OsacForm from '../Form/OsacForm';
type UserPreferencesModalProps = {
  onClose: VoidFunction;
};

const UserPreferencesModal: React.FC<UserPreferencesModalProps> = ({ onClose }) => {
  const { t } = useTranslation();
  const { userTheme, setUserTheme, userContrast, setUserContrast } = useSession();

  const colorSchemeOptions: { value: Theme; label: string; icon: ReactNode }[] = [
    { value: 'system', label: t('System'), icon: <RhUiDesktopIcon /> },
    { value: 'light', label: t('Light'), icon: <RhUiLightModeFillIcon /> },
    { value: 'dark', label: t('Dark'), icon: <RhUiDarkModeFillIcon /> },
  ];

  const contrastOptions: { value: Contrast; label: string; icon?: ReactNode }[] = [
    {
      value: 'system',
      label: t('System'),
      icon: <RhUiDesktopIcon />,
    },
    {
      value: 'default',
      label: t('Default'),
    },
    {
      value: 'contrast',
      label: t('High contrast'),
    },
    {
      value: 'glass',
      label: t('Glass'),
    },
  ];

  return (
    <Modal isOpen variant="small" onClose={onClose}>
      <ModalHeader
        title={t('User preferences')}
        description={t('Choose how this application looks for you. Changes apply immediately.')}
      />
      <ModalBody>
        <OsacForm>
          <FormGroup label={t('Color scheme')}>
            <ToggleGroup aria-label={t('Color scheme')}>
              {colorSchemeOptions.map((option) => (
                <ToggleGroupItem
                  key={option.value}
                  buttonId={`user-preferences-theme-${option.value}`}
                  text={option.label}
                  icon={option.icon}
                  isSelected={userTheme === option.value}
                  onChange={(_, selected) => {
                    if (selected) {
                      setUserTheme(option.value);
                    }
                  }}
                />
              ))}
            </ToggleGroup>
          </FormGroup>
          <FormGroup label={t('Contrast mode')}>
            <ToggleGroup aria-label={t('Contrast mode')}>
              {contrastOptions.map((option) => (
                <ToggleGroupItem
                  key={option.value}
                  buttonId={`user-preferences-contrast-${option.value}`}
                  text={option.label}
                  icon={option.icon}
                  isSelected={userContrast === option.value}
                  onChange={(_, selected) => {
                    if (selected) {
                      setUserContrast(option.value);
                    }
                  }}
                />
              ))}
            </ToggleGroup>
          </FormGroup>
        </OsacForm>
      </ModalBody>
    </Modal>
  );
};

export default UserPreferencesModal;
