import { useMemo } from 'react';

import { type BareMetalInstance, BareMetalInstanceState } from '@osac/types';
import { bareMetalInstanceAttachmentsFilter } from '@osac/ui-components/api/v1/external-ip-data';

import { buildBareMetalSshHostByInstanceId } from './bareMetalInstanceDisplay';
import { useExternalIPAttachments } from '../../api/v1/external-ip';

const runningBareMetalInstanceIds = (instances: readonly BareMetalInstance[]): string[] =>
  instances
    .filter(
      (instance) =>
        instance.status?.state === BareMetalInstanceState.RUNNING && Boolean(instance.id),
    )
    .map((instance) => instance.id);

export const useBareMetalSshHosts = (
  instances: readonly BareMetalInstance[],
): ReadonlyMap<string, string> => {
  const runningInstanceIds = useMemo(() => runningBareMetalInstanceIds(instances), [instances]);
  const filter = useMemo(
    () => bareMetalInstanceAttachmentsFilter(runningInstanceIds),
    [runningInstanceIds],
  );

  const { data: externalIpAttachments = [] } = useExternalIPAttachments(
    { filter },
    { enabled: runningInstanceIds.length > 0 },
  );

  return useMemo(
    () => buildBareMetalSshHostByInstanceId(externalIpAttachments),
    [externalIpAttachments],
  );
};
