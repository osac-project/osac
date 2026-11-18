import { FormSection, Stack, StackItem, Title } from '@patternfly/react-core';

import BigIntField from '@osac/ui-components/components/Form/base/BigIntField';

import { useTranslation } from '../../../../hooks/useTranslation';
import IntegerField from '../../../Form/base/IntegerField';
import TextField from '../../../Form/base/TextField';
import OsacForm from '../../../Form/OsacForm';
const CpuMemoryStep = () => {
  const { t } = useTranslation();
  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('CPU & Memory')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          <FormSection title={t('CPU')}>
            <IntegerField
              name="spec.cpu.cores"
              label={t('Cores')}
              fieldId="baremetal-instance-type-cpu-cores"
              isRequired
            />
            <TextField
              name="spec.cpu.architecture"
              label={t('Architecture')}
              fieldId="baremetal-instance-type-cpu-architecture"
              isRequired
              placeholder={t('e.g. x86_64, aarch64')}
            />
            <TextField
              name="spec.cpu.model"
              label={t('Model')}
              fieldId="baremetal-instance-type-cpu-model"
            />
            <IntegerField
              name="spec.cpu.threadsPerCore"
              label={t('Threads per core')}
              fieldId="baremetal-instance-type-cpu-threads-per-core"
              isRequired
            />
          </FormSection>
          <FormSection title={t('Memory')}>
            <BigIntField
              name="spec.memory.totalGb"
              label={t('Total (GB)')}
              fieldId="baremetal-instance-type-memory-total"
              isRequired
            />
            <TextField
              name="spec.memory.type"
              label={t('Type')}
              fieldId="baremetal-instance-type-memory-type"
              placeholder={t('e.g. DDR4, DDR5')}
            />
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default CpuMemoryStep;
