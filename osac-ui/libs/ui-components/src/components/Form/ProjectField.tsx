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
}

const fieldId = 'metadata.project';

const ProjectField = ({ label, onSelect: onSelectProp, isDisabled }: ProjectFieldProps) => {
  const { t } = useTranslation();
  const [field, meta, helpers] = useField<string>('metadata.project');

  const showValidationErrors = useShowFieldValidationErrors();
  const fieldError = getVisibleFieldError(meta, showValidationErrors);

  const fieldLabel = label || t('Project');
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, fieldError);
  const {
    data: projects = [],
    isLoading,
    error: loadError,
  } = useListAllResources(
    Projects,
    cel<Project>((builder) => builder.field('metadata.tenant').notEquals('shared')),
  );

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
