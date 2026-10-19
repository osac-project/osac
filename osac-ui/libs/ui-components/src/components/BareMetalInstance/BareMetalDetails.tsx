import {
  Card,
  CardBody,
  CardTitle,
  Divider,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import {
  type BareMetalInstance,
  BareMetalInstanceConditionType,
  ConditionStatus,
} from '@osac/types';

import BareMetalActionButtons from './BareMetalActionButtons';
import BareMetalDetailsCard from './BareMetalDetailsCard';
import BareMetalNetworkingCard from './BareMetalNetworkingCard';
import BareMetalProvisioningProgress from './BareMetalProvisioningProgress';
import { BareMetalStatusLabel } from './BareMetalStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceConditionsTable } from '../Resource/ResourceConditionsTable';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';

interface Props {
  instance: BareMetalInstance;
}

const BareMetalDetails = ({ instance }: Props) => {
  const { t } = useTranslation();
  const conditions = instance.status?.conditions ?? [];

  const isReady =
    conditions.find((c) => c.type === BareMetalInstanceConditionType.READY)?.status ===
    ConditionStatus.TRUE;

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parentTo="/bare-metal"
                  parentLabel={t('Bare Metal')}
                  resourceName={instance.metadata?.name ?? instance.id}
                  titleAddon={<BareMetalStatusLabel state={instance.status?.state} />}
                />
              </FlexItem>
              <FlexItem>
                <BareMetalActionButtons instance={instance} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          {!isReady && !instance.metadata?.deletionTimestamp && (
            <StackItem>
              <BareMetalProvisioningProgress instance={instance} />
            </StackItem>
          )}
          <StackItem>
            <Grid hasGutter>
              <GridItem md={6}>
                <BareMetalDetailsCard instance={instance} />
              </GridItem>
              <GridItem md={6}>
                <Card isFullHeight>
                  <CardTitle>{t('Conditions')}</CardTitle>
                  <CardBody>
                    <ResourceConditionsTable
                      ariaLabel={t('Bare metal instance conditions')}
                      conditions={conditions}
                      conditionResourceKind="bare_metal_instance"
                    />
                  </CardBody>
                </Card>
              </GridItem>
            </Grid>
          </StackItem>
          <StackItem>
            <BareMetalNetworkingCard instance={instance} />
          </StackItem>
        </Stack>
      </PageSection>
    </>
  );
};

export default BareMetalDetails;
