import {
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Flex,
  FlexItem,
  FormGroup,
  Gallery,
  GalleryItem,
  Label,
} from '@patternfly/react-core';
import { useField } from 'formik';

import { GuestOSFamily } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { GuestOsIcon, type OsType } from '../../shared/GuestOsIcon';

interface GuestOsFamilyOption {
  value: GuestOSFamily;
  title: string;
  description: string;
  icon?: OsType;
}

interface GuestOsFamilyFieldProps {
  name: string;
  isDisabled?: boolean;
  onSelect?: (value: GuestOSFamily) => void;
}

const GuestOsFamilyField = ({ name, isDisabled = false, onSelect }: GuestOsFamilyFieldProps) => {
  const { t } = useTranslation();
  const [field, , helpers] = useField<GuestOSFamily | undefined>(name);

  const options: GuestOsFamilyOption[] = [
    {
      value: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
      title: t('Linux'),
      description: t('Linux-based guest operating systems'),
      icon: 'linux',
    },
    {
      value: GuestOSFamily.GUEST_OS_FAMILY_WINDOWS,
      title: t('Microsoft Windows'),
      description: t('Windows-based guest operating systems'),
      icon: 'windows',
    },
  ];

  return (
    <FormGroup
      role="radiogroup"
      label={t('Select guest operating system')}
      fieldId={`${name}-guest-os-family`}
      isRequired
    >
      <Gallery hasGutter minWidths={{ default: '30%' }}>
        {options.map((option) => {
          const isSelected = field.value === option.value;
          const cardId = `${option.value}`;

          return (
            <GalleryItem key={option.value}>
              <Card
                id={cardId}
                isCompact
                isFullHeight
                isSelectable
                isSelected={isSelected}
                isDisabled={isDisabled}
                onClick={() => {
                  if (!isDisabled) {
                    void helpers.setValue(option.value);
                    onSelect?.(option.value);
                  }
                }}
              >
                <CardHeader
                  selectableActions={{
                    selectableActionId: `${cardId}-input`,
                    selectableActionAriaLabelledby: `${cardId}-title`,
                    name,
                    variant: 'single',
                    onChange: (_event, checked) => {
                      if (checked && !isDisabled) {
                        void helpers.setValue(option.value);
                        onSelect?.(option.value);
                      }
                    },
                    hasNoOffset: true,
                    isHidden: true,
                  }}
                >
                  <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
                    {option.icon ? (
                      <FlexItem>
                        <GuestOsIcon os={option.icon} size="lg" />
                      </FlexItem>
                    ) : null}
                    <FlexItem>
                      <CardTitle id={`${cardId}-title`}>
                        <Flex
                          justifyContent={{ default: 'justifyContentSpaceBetween' }}
                          alignItems={{ default: 'alignItemsCenter' }}
                        >
                          <FlexItem>{option.title}</FlexItem>
                          {isSelected && (
                            <FlexItem>
                              <Label color="grey">{t('Selected')}</Label>
                            </FlexItem>
                          )}
                        </Flex>
                      </CardTitle>
                    </FlexItem>
                  </Flex>
                </CardHeader>
                <CardBody>{option.description}</CardBody>
              </Card>
            </GalleryItem>
          );
        })}
      </Gallery>
    </FormGroup>
  );
};

export default GuestOsFamilyField;
