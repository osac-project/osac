import type { ReactNode } from 'react';
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

import { Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';

import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from './constants';
import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper } from './FormFieldHelper';
import { InputField } from './InputField';
import SecretSelectionField from './SecretSelectionField';
import { useTranslation } from '../../hooks/useTranslation';

interface UserDataFieldProps {
  sourceName: string;
  secretRefName: string;
  inlineName: string;
  projectName: string;
  isDisabled?: boolean;
  isRequired?: boolean;
  labelInfo?: ReactNode;
}

interface UserDataSourceOption {
  value: 'inline' | 'secret';
  title: string;
  description: string;
}

const UserDataField = ({
  sourceName,
  secretRefName,
  inlineName,
  projectName,
  isDisabled = false,
  isRequired = false,
  labelInfo,
}: UserDataFieldProps) => {
  const { t } = useTranslation();
  const [sourceField, sourceMeta, sourceHelpers] = useField<string>(sourceName);
  const showValidationErrors = useShowFieldValidationErrors();
  const currentSource = sourceField.value === 'secret' ? 'secret' : 'inline';
  const sourceError = getVisibleFieldError(sourceMeta, showValidationErrors);
  const options: UserDataSourceOption[] = [
    {
      value: 'inline',
      title: t('Enter value'),
      description: t('Paste cloud-init user data'),
    },
    {
      value: 'secret',
      title: t('Select secret'),
      description: t('Use a USER_DATA secret from this project'),
    },
  ];

  const selectSource = (value: UserDataSourceOption['value']) => {
    void sourceHelpers.setValue(value);
  };

  return (
    <>
      <FormGroup
        role="radiogroup"
        label={t('User data')}
        fieldId="user-data-source"
        isRequired={isRequired}
        labelInfo={labelInfo}
      >
        <Gallery hasGutter minWidths={{ default: '30%' }}>
          {options.map((option) => {
            const isSelected = currentSource === option.value;
            const cardId = `user-data-source-${option.value}`;
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
                      selectSource(option.value);
                    }
                  }}
                >
                  <CardHeader
                    selectableActions={{
                      selectableActionId: `${cardId}-input`,
                      selectableActionAriaLabelledby: `${cardId}-label`,
                      name: sourceName,
                      variant: 'single',
                      onChange: (_event, checked) => {
                        if (checked && !isDisabled) {
                          selectSource(option.value);
                        }
                      },
                      hasNoOffset: true,
                      isHidden: true,
                    }}
                  >
                    <span id={`${cardId}-label`} className="pf-v6-screen-reader">
                      {option.title}
                    </span>
                    <CardTitle>
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
                  </CardHeader>
                  <CardBody>{option.description}</CardBody>
                </Card>
              </GalleryItem>
            );
          })}
        </Gallery>
        <FormFieldHelper error={sourceError} fieldId="user-data-source" />
      </FormGroup>
      {currentSource === 'secret' ? (
        <SecretSelectionField
          label={t('User data secret')}
          filter={cel<Secret>((filter) =>
            filter.and(
              filter.field('metadata.project').equals(projectName),
              filter.field('type').equals(SecretType.USER_DATA),
            ),
          )}
          name={secretRefName}
          placeholder={t('Select a user data secret')}
          isDisabled={isDisabled}
          asFormGroup={false}
        />
      ) : (
        <InputField
          label={t('Cloud-init')}
          name={inlineName}
          fieldId="inline-user-data"
          multiline
          rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
          resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
          helperText={t('Optional cloud-init user data (max 64 KB).')}
          isDisabled={isDisabled}
          asFormGroup={false}
        />
      )}
    </>
  );
};

export default UserDataField;
