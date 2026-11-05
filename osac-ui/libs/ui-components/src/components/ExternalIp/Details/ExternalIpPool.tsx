import { Content, Flex, FlexItem } from '@patternfly/react-core';

import { type ExternalIP, type ExternalIPPool } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';

interface ExternalIpPoolProps {
  externalIp: ExternalIP;
  pool?: ExternalIPPool;
}

const ExternalIpPool = ({ externalIp, pool }: ExternalIpPoolProps) => {
  const { t } = useTranslation();
  const available = pool?.status?.available;
  const poolName = externalIp.spec?.pool?.name || pool?.metadata?.name;

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
