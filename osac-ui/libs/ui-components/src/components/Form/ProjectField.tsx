import { useMemo } from 'react';
import { Alert, FormGroup } from '@patternfly/react-core';
import { useField } from 'formik';

import { type Project, Projects } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListAllResources } from '@osac/ui-components/api/use-resource';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from './FormFieldHelper';
import ProjectSelect from '../Project/ProjectSelect';

interface ProjectFieldProps {
  label?: string;
  onSelect?: (value: string | number) => void;
  isDisabled?: boolean;
  /**
   * Restricts the project list to one tenant. Cloud provider admins select the
   * tenant explicitly; tenant users inherit it from their session.
   */
  tenant?: string;
}

const fieldId = 'metadata.project';

const ProjectField = ({ label, onSelect: onSelectProp, isDisabled, tenant }: ProjectFieldProps) => {
  const { t } = useTranslation();
  const [field, meta, helpers] = useField<string>('metadata.project');

  const showValidationErrors = useShowFieldValidationErrors();
  const fieldError = getVisibleFieldError(meta, showValidationErrors);

  const fieldLabel = label || t('Project');
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, fieldError);
  const filter = useMemo(
    () =>
      cel<Project>((builder) =>
        builder.and(
          builder.field('metadata.tenant').notEquals('shared'),
          tenant ? builder.field('metadata.tenant').equals(tenant) : undefined,
        ),
      ),
    [tenant],
  );
  const {
    data = [],
    isLoading: isListLoading,
    isPlaceholderData,
    error: loadError,
  } = useListAllResources(Projects, filter, { enabled: tenant !== '' });
  // After a tenant change, the previous tenant's list stays as placeholder data
  // until the new one arrives; never offer those projects.
  const projects = isPlaceholderData ? [] : data;
  // A cleared tenant disables the query; its old placeholder data is not a load in progress.
  const isLoading = isListLoading || (isPlaceholderData && tenant !== '');

  return (
    <>
      <FormGroup label={fieldLabel} fieldId={fieldId} isRequired>
        <ProjectSelect
          mode="single"
          projects={projects}
          selected={[field.value]}
          onChange={(value) => {
            void helpers.setValue(value[0], true);
            void helpers.setTouched(true, false);
            onSelectProp?.(value[0]);
          }}
          isLoading={isLoading}
          error={loadError}
          fieldId={fieldId}
          label={fieldLabel}
          describedBy={helperDescribedBy}
          isDisabled={isDisabled}
        />
        <FormFieldHelper error={fieldError} fieldId={fieldId} />
      </FormGroup>
      {!!loadError && (
        <Alert variant="danger" isInline title={t('Failed to fetch projects')}>
          {getErrorMessage(loadError)}
        </Alert>
      )}
    </>
  );
};

export default ProjectField;
