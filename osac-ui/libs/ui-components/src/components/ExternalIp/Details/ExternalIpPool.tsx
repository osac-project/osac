import { Content, Flex, FlexItem } from '@patternfly/react-core';

import { type ExternalIP, type ExternalIPPool } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';

interface ExternalIpPoolProps {
  externalIp: ExternalIP;
  pool?: ExternalIPPool;
}

const ExternalIpPool = ({ externalIp, pool }: ExternalIpPoolProps) => {
  const { t } = useTranslation();
  const poolName =
    pool?.metadata?.name ||
    externalIp.spec?.pool?.name ||
    externalIp.spec?.pool?.id ||
    externalIp.status?.pool ||
    '—';
  const available = pool?.status?.available;

  return (
    <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsXs' }}>
      <FlexItem>{poolName === '' ? t('Default') : poolName || '—'}</FlexItem>
      {available !== undefined ? (
        <FlexItem>
          <Content component="small">
            {t('{{available}} available', { available: available.toString() })}
          </Content>
        </FlexItem>
      ) : null}
    </Flex>
  );
};

export default ExternalIpPool;
