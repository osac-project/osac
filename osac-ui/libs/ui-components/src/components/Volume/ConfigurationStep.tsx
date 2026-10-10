import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { StorageProtocol, VolumeAccessMode } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { InputField } from '../Form/InputField';
import OsacForm from '../Form/OsacForm';
import { SelectField } from '../Form/SelectField';
import { StorageTierSelectField } from '../Form/StorageTierSelectField';

const ConfigurationStep = () => {
  const { t } = useTranslation();
  const accessModeOptions = [
    { value: VolumeAccessMode.READ_WRITE_ONCE, label: t('ReadWriteOnce') },
    { value: VolumeAccessMode.READ_ONLY_MANY, label: t('ReadOnlyMany') },
    { value: VolumeAccessMode.READ_WRITE_MANY, label: t('ReadWriteMany') },
    { value: VolumeAccessMode.READ_WRITE_ONCE_POD, label: t('ReadWriteOncePod') },
  ];

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">{t('Configure the volume storage and access settings.')}</Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <StorageTierSelectField
            name="spec.storageTier"
            label={t('Storage tier')}
            fieldId="spec.storageTier"
            protocol={StorageProtocol.BLOCK}
            isRequired
          />
          <InputField
            name="spec.sizeGib"
            label={t('Size (GiB)')}
            fieldId="spec.sizeGib"
            type="number"
            inputMode="numeric"
            min={1}
            step={1}
            isRequired
          />
          <SelectField
            name="spec.accessMode"
            label={t('Access Mode')}
            fieldId="spec.accessMode"
            options={accessModeOptions}
            placeholder={t('Select an access mode')}
            isRequired
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ConfigurationStep;
