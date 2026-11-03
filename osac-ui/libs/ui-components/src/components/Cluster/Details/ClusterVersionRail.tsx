import { useId } from 'react';
import {
  Button,
  Card,
  CardBody,
  CardTitle,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Label,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';

export type UpgradeActionState =
  | { kind: 'loading' }
  | { kind: 'available' }
  | { kind: 'blocked'; reason: string };

export interface ClusterVersionRailProps {
  currentVersion?: string;
  action: UpgradeActionState;
  onUpgrade: () => void;
}

const ClusterVersionRail = ({ currentVersion, action, onUpgrade }: ClusterVersionRailProps) => {
  const { t } = useTranslation();
  const reasonId = useId();
  const unavailableReason =
    action.kind === 'blocked'
      ? action.reason
      : action.kind === 'loading'
        ? t('Checking upgrade options')
        : undefined;

  return (
    <Card variant="secondary">
      <CardTitle>{t('Cluster version')}</CardTitle>
      <CardBody>
        <Stack hasGutter>
          <StackItem>
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Current version')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {currentVersion?.trim() || '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </StackItem>
          {action.kind === 'available' ? (
            <StackItem>
              <Label color="green">{t('Upgrade available')}</Label>
            </StackItem>
          ) : action.kind === 'blocked' ? (
            <StackItem>
              <Label color="grey">{t('No upgrade available')}</Label>
            </StackItem>
          ) : null}
          <StackItem>
            <Button
              variant="primary"
              onClick={onUpgrade}
              isDisabled={action.kind !== 'available'}
              aria-describedby={unavailableReason ? reasonId : undefined}
              aria-busy={action.kind === 'loading' || undefined}
            >
              {t('Upgrade cluster')}
            </Button>
          </StackItem>
          {unavailableReason ? (
            <StackItem>
              <Content component="p" id={reasonId}>
                {unavailableReason}
              </Content>
            </StackItem>
          ) : null}
        </Stack>
      </CardBody>
    </Card>
  );
};

export default ClusterVersionRail;
