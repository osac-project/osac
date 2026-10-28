import {
  Card,
  CardBody,
  CardHeader,
  FormGroup,
  Grid,
  GridItem,
  Label,
  Split,
  SplitItem,
} from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';

interface NetworkSelectionCardsProps {
  fieldName: string;
  useDefaultNetwork: boolean;
  isDisabled: boolean;
  showSecurityGroups: boolean;
  onSelect: (useDefault: boolean) => void;
}

const NetworkSelectionCards = ({
  fieldName,
  useDefaultNetwork,
  isDisabled,
  showSecurityGroups,
  onSelect,
}: NetworkSelectionCardsProps) => {
  const { t } = useTranslation();
  const choiceId = `${fieldName}-choice`;
  const defaultId = `${choiceId}-default`;
  const customId = `${choiceId}-custom`;

  return (
    <FormGroup label={t('Network')} isRequired fieldId={choiceId} role="radiogroup">
      <Grid id={choiceId} hasGutter>
        <GridItem lg={6}>
          <Card isSelectable isSelected={useDefaultNetwork} isDisabled={isDisabled} isFullHeight>
            <CardHeader
              selectableActions={{
                variant: 'single',
                isHidden: true,
                name: choiceId,
                selectableActionId: defaultId,
                selectableActionAriaLabelledby: `${defaultId}-title`,
                onChange: (_event, checked) => checked && onSelect(true),
              }}
            >
              <Split hasGutter>
                <SplitItem isFilled>
                  <strong id={`${defaultId}-title`}>{t('Tenant default')}</strong>
                </SplitItem>
                {useDefaultNetwork && (
                  <SplitItem>
                    <Label color="grey" isCompact>
                      {t('Selected')}
                    </Label>
                  </SplitItem>
                )}
              </Split>
            </CardHeader>
            <CardBody>{t('Network created with your tenant.')}</CardBody>
          </Card>
        </GridItem>
        <GridItem lg={6}>
          <Card isSelectable isSelected={!useDefaultNetwork} isDisabled={isDisabled} isFullHeight>
            <CardHeader
              selectableActions={{
                variant: 'single',
                isHidden: true,
                name: choiceId,
                selectableActionId: customId,
                selectableActionAriaLabelledby: `${customId}-title`,
                onChange: (_event, checked) => checked && onSelect(false),
              }}
            >
              <Split hasGutter>
                <SplitItem isFilled>
                  <strong id={`${customId}-title`}>{t('Custom network')}</strong>
                </SplitItem>
                {!useDefaultNetwork && (
                  <SplitItem>
                    <Label color="grey" isCompact>
                      {t('Selected')}
                    </Label>
                  </SplitItem>
                )}
              </Split>
            </CardHeader>
            <CardBody>
              {showSecurityGroups
                ? t('Choose a virtual network, subnet, and security groups.')
                : t('Choose a virtual network and subnet.')}
            </CardBody>
          </Card>
        </GridItem>
      </Grid>
    </FormGroup>
  );
};

export default NetworkSelectionCards;
