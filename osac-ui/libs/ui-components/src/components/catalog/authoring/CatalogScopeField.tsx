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
  Icon,
  Label,
} from '@patternfly/react-core';
import BuildingIcon from '@patternfly/react-icons/dist/esm/icons/building-icon';
import GlobeAmericasIcon from '@patternfly/react-icons/dist/esm/icons/globe-americas-icon';
import { useFormikContext } from 'formik';

import type { CatalogGeneralValues, CatalogItemScope } from './CatalogGeneralStep';
import { useTranslation } from '../../../hooks/useTranslation';

interface ScopeOption {
  value: CatalogItemScope;
  title: string;
  description: string;
  icon: typeof BuildingIcon;
}

interface CatalogScopeFieldProps {
  isDisabled?: boolean;
}

const CatalogScopeField = ({ isDisabled = false }: CatalogScopeFieldProps) => {
  const { t } = useTranslation();
  const { values, setFieldValue } = useFormikContext<CatalogGeneralValues>();

  const options: ScopeOption[] = [
    {
      value: 'global',
      title: t('Global'),
      description: t('Available to all tenants on the platform'),
      icon: GlobeAmericasIcon,
    },
    {
      value: 'tenant',
      title: t('Tenant scoped'),
      description: t('Assigned to one tenant'),
      icon: BuildingIcon,
    },
  ];

  return (
    <FormGroup role="radiogroup" label={t('Scope')} fieldId="catalog-item-scope" isRequired>
      <Gallery hasGutter minWidths={{ default: '30%' }}>
        {options.map((option) => {
          const isSelected = values.scope === option.value;
          const cardId = `catalog-item-scope-${option.value}`;
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
                  if (isDisabled) {
                    return;
                  }
                  void setFieldValue('scope', option.value);
                }}
              >
                <CardHeader
                  selectableActions={{
                    selectableActionId: `${cardId}-input`,
                    selectableActionAriaLabelledby: `${cardId}-title`,
                    name: 'catalog-item-scope',
                    variant: 'single',
                    onChange: (_event, checked) => {
                      if (checked && !isDisabled) {
                        void setFieldValue('scope', option.value);
                      }
                    },
                    hasNoOffset: true,
                    isHidden: true,
                  }}
                >
                  <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
                    <FlexItem>
                      <Icon size="lg">
                        <option.icon aria-hidden />
                      </Icon>
                    </FlexItem>
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

export default CatalogScopeField;
