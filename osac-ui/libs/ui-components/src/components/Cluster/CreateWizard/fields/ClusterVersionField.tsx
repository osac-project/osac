import { useMemo } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useField } from 'formik';

import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  useClusterVersions,
} from '../../../../api/v1/cluster-versions';
import { useTranslation } from '../../../../hooks/useTranslation';
import { getErrorMessage } from '../../../../utils/error';
import { SelectField } from '../../../Form/SelectField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import { findVersionByName, isDeprecatedVersion, versionDisplayName } from '../versionUtils';

interface ClusterVersionFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const ClusterVersionField = ({
  name,
  fieldId = 'cluster-version',
  isDisabled = false,
}: ClusterVersionFieldProps) => {
  const { t } = useTranslation();
  const {
    data: versions = [],
    isLoading: versionsLoading,
    error: versionsError,
    refetch: refetchVersions,
  } = useClusterVersions({ filter: CLUSTER_VERSION_ACTIVE_LIST_FILTER });

  const [versionField] = useField<string>(name);
  const isSelectedDeprecated = isDeprecatedVersion(findVersionByName(versions, versionField.value));

  const versionOptions = useMemo(
    () =>
      versions.map((version) => {
        const versionName = version.metadata?.name ?? '';
        const label = versionDisplayName(version, versionName);
        return {
          value: versionName,
          label: isDeprecatedVersion(version)
            ? t('{{version}} (deprecated)', { version: label })
            : label,
        };
      }),
    [versions, t],
  );

  return (
    <Stack hasGutter>
      {versionsError ? (
        <StackItem>
          <Alert variant="danger" isInline title={t('Could not load cluster versions')}>
            <Stack hasGutter>
              <StackItem>{getErrorMessage(versionsError)}</StackItem>
              <StackItem>
                <Button variant="link" isInline onClick={() => void refetchVersions()}>
                  {t('Retry')}
                </Button>
              </StackItem>
            </Stack>
          </Alert>
        </StackItem>
      ) : null}
      <StackItem>
        <SelectField
          name={name}
          label={t('Version')}
          fieldId={fieldId}
          isRequired
          isDisabled={isDisabled}
          isLoading={versionsLoading}
          placeholder={t('Select a version')}
          options={versionOptions}
        />
        {isSelectedDeprecated ? (
          <Alert
            variant="warning"
            isInline
            title={t('This version is deprecated and may be removed in a future release.')}
          />
        ) : null}
      </StackItem>
    </Stack>
  );
};

export default ClusterVersionField;
