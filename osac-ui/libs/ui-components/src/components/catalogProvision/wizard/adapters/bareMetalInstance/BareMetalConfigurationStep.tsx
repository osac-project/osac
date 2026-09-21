import { useMemo } from 'react';
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardTitle,
  Gallery,
  GalleryItem,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import {
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceType,
  BareMetalInstanceTypes,
} from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { SelectField } from '@osac/ui-components/components/Form/SelectField';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import {
  BM_INSTANCE_TYPE_WIRE_PATH,
  BM_USER_DATA_FORM_PATH,
  BM_USER_DATA_WIRE_PATH,
  BareMetalInstanceWizardValues,
} from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import OsacForm from '../../../../Form/OsacForm';
import { getCatalogFieldOverlay, readCatalogFieldDefinitions } from '../../catalogOverlay';
import UserDataField from '../../fields/UserDataField';

const InstanceTypeDescription = ({ instanceType }: { instanceType: BareMetalInstanceType }) => {
  const { t } = useTranslation();
  const accelerators = instanceType.spec?.hardware?.accelerators.map(
    (a) => `${a.type} (${a.model}${a.memoryGb ? `, ${a.memoryGb} GB` : ''})`,
  );
  const disks = instanceType.spec?.hardware?.disks.map((a) => `${a.type} (${a.capacityGb})`);

  return (
    <>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('CPU')}</CardTitle>
          <CardBody>
            <Stack>
              <StackItem>
                {t('Model: {{model}}', { model: instanceType.spec?.hardware?.cpu?.model || '-' })}
              </StackItem>
              <StackItem>
                {t('Architecture: {{arch}}', {
                  arch: instanceType.spec?.hardware?.cpu?.architecture || '-',
                })}
              </StackItem>
              <StackItem>
                {t('Cores: {{cores}}', { cores: instanceType.spec?.hardware?.cpu?.cores || '-' })}
              </StackItem>
              <StackItem>
                {t('Threads per core: {{threads}}', {
                  threads: instanceType.spec?.hardware?.cpu?.threadsPerCore || '-',
                })}
              </StackItem>
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('Memory')}</CardTitle>
          <CardBody>
            <Stack>
              <StackItem>
                {t('Total GB: {{total}}', {
                  total: instanceType.spec?.hardware?.memory?.totalGb || '-',
                })}
              </StackItem>
              <StackItem>
                {t('Type: {{type}}', { type: instanceType.spec?.hardware?.memory?.type || '-' })}
              </StackItem>
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('Accelerators')}</CardTitle>
          <CardBody>
            <Stack>
              {accelerators?.length
                ? accelerators.map((a, idx) => <StackItem key={idx}>{a}</StackItem>)
                : '-'}
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('Disks')}</CardTitle>
          <CardBody>
            <Stack>
              {disks?.length ? disks.map((d, idx) => <StackItem key={idx}>{d}</StackItem>) : '-'}
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
    </>
  );
};

interface Props {
  catalogItem: BareMetalInstanceCatalogItem | null;
}

const BareMetalConfigurationStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();

  const { data, isLoading, error, refetch } = useListResource(BareMetalInstanceTypes);

  const definitions = useMemo(() => readCatalogFieldDefinitions(catalogItem), [catalogItem]);
  const instanceTypeOverlay = useMemo(
    () => getCatalogFieldOverlay(BM_INSTANCE_TYPE_WIRE_PATH, definitions, t('Instance type')),
    [definitions, t],
  );

  const currentInstanceType = data?.items.find(
    (i) => i.metadata?.name === values.spec.instanceType.name,
  );

  return (
    <Stack hasGutter>
      {!!error && (
        <StackItem>
          <Alert variant="danger" isInline title={t('Could not load instance types')}>
            <Stack hasGutter>
              <StackItem>{getErrorMessage(error)}</StackItem>
              <StackItem>
                <Button variant="link" isInline onClick={() => void refetch()}>
                  {t('Retry')}
                </Button>
              </StackItem>
            </Stack>
          </Alert>
        </StackItem>
      )}
      <StackItem>
        <OsacForm>
          <SelectField
            name="spec.instanceType.name"
            label={t('Instance type')}
            fieldId="instance-type"
            isRequired
            isLoading={isLoading}
            isDisabled={!instanceTypeOverlay.editable || !!error}
            placeholder={t('Select an instance type')}
            options={(data?.items || []).map((instanceType) => ({
              label: `${instanceType.metadata?.name || ''}`,
              value: instanceType.metadata?.name || '',
              description: instanceType.spec?.description,
            }))}
          />
        </OsacForm>
      </StackItem>
      <StackItem>
        <Gallery hasGutter>
          {currentInstanceType && <InstanceTypeDescription instanceType={currentInstanceType} />}
          <GalleryItem>
            <Card variant="secondary" isFullHeight>
              <CardTitle>{t('Disk image')}</CardTitle>
              <CardBody>{'-'}</CardBody>
            </Card>
          </GalleryItem>
        </Gallery>
      </StackItem>
      <StackItem>
        <OsacForm>
          <UserDataField
            catalogItem={catalogItem}
            name={BM_USER_DATA_FORM_PATH}
            wirePath={BM_USER_DATA_WIRE_PATH}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default BareMetalConfigurationStep;
