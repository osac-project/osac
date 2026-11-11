import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ActionGroup, Alert, Button, PageSection, Stack, StackItem } from '@patternfly/react-core';
import { Formik } from 'formik';

import { Protocol } from '@osac/types';

import { useSecurityGroup, useUpdateSecurityGroup } from '../../api/v1/networking';
import OsacForm from '../../components/Form/OsacForm';
import { isAutoCreatedResource } from '../../components/networking/resourceOrigin';
import SecurityGroupRuleRows from '../../components/networking/SecurityGroupRuleRows';
import {
  type SecurityGroupRulesFormValues,
  fromRuleFormValues,
  icmpRejectedMessage,
  isIcmpRejection,
  securityGroupRuleEditDisabledReason,
  securityGroupRulesSchema,
  toRuleFormValues,
} from '../../components/networking/securityGroupRuleUtils';
import ListPageBody from '../../components/Page/ListPageBody';
import { ResourceDetailHeader } from '../../components/Resource/ResourceDetailHeader';
import { useToast } from '../../components/Toast/useToast';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';

/**
 * Full-page form for the rules of a security group. Save sends one
 * SecurityGroups.Update with the complete inbound and outbound lists.
 */
export const SecurityGroupEditRulesPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { addToast } = useToast();
  const { id = '' } = useParams<{ id: string }>();
  const { data: sg, isLoading, error } = useSecurityGroup(id);
  const updateSecurityGroup = useUpdateSecurityGroup();
  // After the server rejects ICMP, the ICMP rows stay marked until the user changes them.
  const [icmpRejected, setIcmpRejected] = useState(false);

  const sgName = sg?.metadata?.name || id;
  const detailsPath = `/networking/security-groups/${id}`;
  const isAuto = isAutoCreatedResource(sg);
  const blockedReason = isAuto
    ? t('The rules of an automatic security group cannot be edited.')
    : securityGroupRuleEditDisabledReason(t, sg);

  return (
    <PageSection hasBodyWrapper={false}>
      <Stack hasGutter>
        <StackItem>
          <ResourceDetailHeader
            parents={[
              { to: '/networking/security-groups', label: t('Security groups') },
              { to: detailsPath, label: sgName },
            ]}
            resourceName={t('Edit rules')}
            description={t(
              'Saving replaces every rule of the group on each workload that uses it.',
            )}
          />
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            {sg ? (
              <Formik<SecurityGroupRulesFormValues>
                initialValues={{
                  ingress: (sg.spec?.ingress ?? []).map(toRuleFormValues),
                  egress: (sg.spec?.egress ?? []).map(toRuleFormValues),
                }}
                validationSchema={securityGroupRulesSchema(t, { rejectIcmp: icmpRejected })}
                onSubmit={async (values, { setFieldError }) => {
                  try {
                    await updateSecurityGroup.mutateAsync({
                      object: {
                        id: sg.id,
                        metadata: { name: sg.metadata?.name ?? '' },
                        spec: {
                          virtualNetwork: sg.spec?.virtualNetwork,
                          ingress: values.ingress.map(fromRuleFormValues),
                          egress: values.egress.map(fromRuleFormValues),
                        },
                      },
                    });
                    addToast({
                      variant: 'success',
                      title: t('Updating the rules of {{name}}', { name: sgName }),
                    });
                    navigate(detailsPath);
                  } catch (saveError) {
                    // The message is shown above the form; an ICMP rejection also marks its rows.
                    if (isIcmpRejection(getErrorMessage(saveError))) {
                      setIcmpRejected(true);
                      (['ingress', 'egress'] as const).forEach((direction) =>
                        values[direction].forEach((rule, index) => {
                          if (rule.protocol === Protocol.ICMP) {
                            setFieldError(`${direction}.${index}.protocol`, icmpRejectedMessage(t));
                          }
                        }),
                      );
                    }
                  }
                }}
              >
                {({ submitForm, isSubmitting }) => (
                  <OsacForm>
                    {blockedReason ? (
                      <Alert variant="warning" title={t('Rules cannot be edited now')} isInline>
                        {blockedReason}
                      </Alert>
                    ) : null}
                    {updateSecurityGroup.error ? (
                      <Alert variant="danger" title={t('Failed to save rules')} isInline>
                        {getErrorMessage(updateSecurityGroup.error)}
                      </Alert>
                    ) : null}
                    <SecurityGroupRuleRows
                      direction="ingress"
                      isDisabled={Boolean(blockedReason)}
                    />
                    <SecurityGroupRuleRows direction="egress" isDisabled={Boolean(blockedReason)} />
                    <ActionGroup>
                      <Button
                        variant="primary"
                        onClick={submitForm}
                        isLoading={isSubmitting}
                        isDisabled={isSubmitting || Boolean(blockedReason)}
                      >
                        {t('Save')}
                      </Button>
                      <Button
                        variant="link"
                        onClick={() => navigate(detailsPath)}
                        isDisabled={isSubmitting}
                      >
                        {t('Cancel')}
                      </Button>
                    </ActionGroup>
                  </OsacForm>
                )}
              </Formik>
            ) : null}
          </ListPageBody>
        </StackItem>
      </Stack>
    </PageSection>
  );
};
