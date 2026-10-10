import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { VolumeFormValues } from './values';
import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';

const ReviewStep = () => {
  const { t } = useTranslation();
  const {
    values: {
      metadata: { name, description },
      spec: { storageTier, sizeGib, accessMode },
    },
  } = useFormikContext<VolumeFormValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Review')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Review')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(name)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(description)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Storage tier')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(storageTier.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Size (GiB)')}</DescriptionListTerm>
            <DescriptionListDescription>
              {sizeGib ? `${sizeGib} GiB` : '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Access Mode')}</DescriptionListTerm>
            <DescriptionListDescription>
              <VolumeAccessModeLabel accessMode={accessMode} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ReviewStep;
