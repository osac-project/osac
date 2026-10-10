import React, { useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  PageSection,
  Stack,
  StackItem,
  TextArea,
  TextInput,
  Title,
} from '@patternfly/react-core';
import DownloadIcon from '@patternfly/react-icons/dist/esm/icons/download-icon';
import { EyeIcon } from '@patternfly/react-icons/dist/esm/icons/eye-icon';
import { EyeSlashIcon } from '@patternfly/react-icons/dist/esm/icons/eye-slash-icon';
import { LockIcon } from '@patternfly/react-icons/dist/esm/icons/lock-icon';

import { Secret, SecretType, Secrets } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import SecretDetailsActionButtons from './SecretDetailsActionButtons';
import { Timestamp } from '../../Primitives/Timestamp';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';
import { SubtleContent } from '../../SubtleContent/SubtleContent';
import { decodeSecretValue, getDataEntries } from '../CreatePage/values';
import { downloadSecretBytes, getSecretType } from '../utils';

interface SecretDetailsPageContentProps {
  secret: Secret;
}

const SecretDetailsPageContent = ({ secret }: SecretDetailsPageContentProps) => {
  const { t } = useTranslation();
  const [showValues, setShowValues] = useState(false);
  const secretName = secret.metadata?.name || secret.id;
  const dataEntries = getDataEntries(secret.data);

  const secretTypes = getSecretType(t);

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
                  parentTo="/secrets"
                  parentLabel={t('Secrets')}
                  resourceName={secretName}
                />
              </FlexItem>
              <FlexItem>
                <SecretDetailsActionButtons secret={secret} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Grid hasGutter>
          <GridItem md={6}>
            <Stack hasGutter>
              <StackItem>
                <Title headingLevel="h5">{t('Overview')}</Title>
                {secret.metadata?.description && (
                  <SubtleContent>{secret.metadata?.description}</SubtleContent>
                )}
              </StackItem>
              <StackItem>
                <DescriptionList>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {secret.metadata?.project || t('Default')}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Type')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {secretTypes[secret.type] || secretTypes[SecretType.UNSPECIFIED]}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      <Timestamp value={secret.metadata?.creationTimestamp} />
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                </DescriptionList>
              </StackItem>
            </Stack>
          </GridItem>
          <GridItem md={6}>
            <Stack hasGutter>
              <StackItem>
                <Flex justifyContent={{ default: 'justifyContentSpaceBetween' }}>
                  <FlexItem>
                    <Title headingLevel="h5">{t('Secret data')}</Title>
                  </FlexItem>
                  <FlexItem>
                    <Button
                      variant="link"
                      icon={showValues ? <EyeSlashIcon /> : <EyeIcon />}
                      onClick={() => setShowValues((v) => !v)}
                    >
                      {showValues ? t('Hide values') : t('Reveal values')}
                    </Button>
                  </FlexItem>
                </Flex>
              </StackItem>
              <StackItem>
                <DescriptionList>
                  {dataEntries.map((entry) => (
                    <React.Fragment key={entry.key}>
                      <DescriptionListGroup>
                        <DescriptionListTerm>{t('Key')}</DescriptionListTerm>
                        <DescriptionListDescription>{entry.key}</DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>{t('Value')}</DescriptionListTerm>
                        <DescriptionListDescription>
                          {showValues ? (
                            decodeSecretValue(entry.value) !== undefined ? (
                              <TextArea
                                id={`secret-value-${entry.key}`}
                                aria-label={t('Secret value for {{key}}', { key: entry.key })}
                                value={decodeSecretValue(entry.value)}
                                readOnly
                                rows={4}
                              />
                            ) : (
                              <Stack>
                                <StackItem>
                                  {t(
                                    'This value contains binary data and cannot be displayed as text. Download it to access the raw data.',
                                  )}
                                </StackItem>
                                <StackItem>
                                  <Button
                                    variant="link"
                                    icon={<DownloadIcon />}
                                    onClick={() => {
                                      if (entry.value) {
                                        downloadSecretBytes(entry.value, entry.key);
                                      }
                                    }}
                                  >
                                    {t('Download value')}
                                  </Button>
                                </StackItem>
                              </Stack>
                            )
                          ) : (
                            <TextInput
                              value="********"
                              type="text"
                              readOnlyVariant="default"
                              customIcon={<LockIcon />}
                            />
                          )}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                    </React.Fragment>
                  ))}
                </DescriptionList>
              </StackItem>
            </Stack>
          </GridItem>
        </Grid>
      </PageSection>
    </>
  );
};

const SecretDetailsPage = () => {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, error, refetch } = useGetResource(
    Secrets,
    { id: id ?? '' },
    { enabled: Boolean(id) },
  );

  return (
    <ResourceDetailsPage
      error={error}
      found={!!data?.object}
      isLoading={isLoading}
      parentLabel={t('Secrets')}
      parentTo="/secrets"
      refetch={refetch}
    >
      {data?.object && <SecretDetailsPageContent secret={data.object} />}
    </ResourceDetailsPage>
  );
};

export default SecretDetailsPage;
