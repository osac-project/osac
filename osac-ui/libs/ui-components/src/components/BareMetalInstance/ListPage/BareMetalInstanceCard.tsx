import { useMemo, useState } from 'react';
import {
  Button,
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  CardTitle,
  Divider,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import {
  type BareMetalInstance,
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceState,
  BareMetalInstanceType,
} from '@osac/types';
import BareMetalInstanceResources from '@osac/ui-components/components/BareMetalInstance/ListPage/BareMetalInstanceResources';
import {
  findBareMetalInstanceTypeForReference,
  findBareMetalInstanceTypeReference,
} from '@osac/ui-components/components/catalog/bareMetalCatalogItemResourceDisplay';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { CatalogItemIcon } from '@osac/ui-components/icons';

import { BareMetalActionsMenu } from './BareMetalActionsMenu';
import BareMetalConnectSshModal from '../BareMetalConnectSshModal';
import { formatBareMetalCreatedAt } from '../bareMetalInstanceDisplay';
import { BareMetalStatusLabel } from '../BareMetalStatusLabel';

import './BareMetalInstanceCard.css';

interface BareMetalInstanceCardProps {
  instance: BareMetalInstance;
  instanceTypes?: BareMetalInstanceType[];
  catalogItems?: BareMetalInstanceCatalogItem[];
  sshHost?: string;
}

const BareMetalInstanceCard = ({
  instance,
  instanceTypes = [],
  catalogItems,
  sshHost,
}: BareMetalInstanceCardProps) => {
  const { t } = useTranslation();
  const { username } = useSession();
  const [connectSshOpen, setConnectSshOpen] = useState(false);

  const instanceType = useMemo(() => {
    const reference = findBareMetalInstanceTypeReference(instance, catalogItems);
    return findBareMetalInstanceTypeForReference(instanceTypes, reference);
  }, [catalogItems, instance, instanceTypes]);

  const canConnectSsh =
    instance.status?.state === BareMetalInstanceState.RUNNING && Boolean(sshHost);
  const createdAt = formatBareMetalCreatedAt(instance.metadata);

  return (
    <>
      {connectSshOpen && sshHost && (
        <BareMetalConnectSshModal
          host={sshHost}
          username={username}
          onClose={() => setConnectSshOpen(false)}
        />
      )}
      <Card isFullHeight className="bare-metal-instance-card">
        <CardHeader
          className="bare-metal-instance-card__header"
          actions={{
            actions: (
              <Flex
                alignItems={{ default: 'alignItemsCenter' }}
                spaceItems={{ default: 'spaceItemsSm' }}
              >
                <FlexItem>
                  <BareMetalStatusLabel state={instance.status?.state} />
                </FlexItem>
                <FlexItem>
                  <BareMetalActionsMenu instance={instance} sshHost={sshHost} />
                </FlexItem>
              </Flex>
            ),
          }}
        >
          <CatalogItemIcon kind="bm" />
        </CardHeader>
        <CardTitle>
          <ResourceNameField
            resource={instance}
            detailsUrl={`/bare-metal/${instance.id}`}
            subTitle={instance.spec?.catalogItem?.name}
          />
        </CardTitle>
        <CardBody>
          <BareMetalInstanceResources instance={instance} instanceType={instanceType} />
        </CardBody>
        <Divider />
        <CardFooter>
          <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
            <FlexItem>
              <Flex
                flexWrap={{ default: 'nowrap' }}
                gap={{ default: 'gapLg' }}
                justifyContent={{ default: 'justifyContentSpaceBetween' }}
              >
                <FlexItem>{t('Project')}</FlexItem>
                <FlexItem>{instance.metadata?.project || t('Default')}</FlexItem>
              </Flex>
            </FlexItem>
            <FlexItem>
              <Flex
                flexWrap={{ default: 'nowrap' }}
                gap={{ default: 'gapLg' }}
                justifyContent={{ default: 'justifyContentSpaceBetween' }}
              >
                <FlexItem>{t('Created')}</FlexItem>
                <FlexItem>{createdAt ?? '—'}</FlexItem>
              </Flex>
            </FlexItem>
          </Flex>
          <Button
            className="pf-v6-u-mt-md"
            variant="primary"
            isBlock
            isDisabled={!canConnectSsh}
            onClick={() => setConnectSshOpen(true)}
          >
            {t('Connect via SSH')}
          </Button>
        </CardFooter>
      </Card>
    </>
  );
};

export default BareMetalInstanceCard;
