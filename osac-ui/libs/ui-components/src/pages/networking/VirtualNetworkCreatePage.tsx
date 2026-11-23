import { useEffect, useMemo, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, PageSection, Stack, StackItem } from '@patternfly/react-core';
import { Formik, useFormikContext } from 'formik';
import * as Yup from 'yup';

import { type VirtualNetwork, VirtualNetworks } from '@osac/types';

import { useCreateResource } from '../../api/use-resource';
import TextField from '../../components/Form/base/TextField';
import NameField from '../../components/Form/NameField';
import OsacForm from '../../components/Form/OsacForm';
import ProjectField from '../../components/Form/ProjectField';
import ListPage from '../../components/Page/ListPage';
import { useToast } from '../../components/Toast/useToast';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { buildCidrSchema } from '../../validation/cidr-validation';

type VirtualNetworkCreateFormValues = {
  metadata: {
    name: string;
    project: string;
  };
  ipv4Cidr: string;
};

/** Clears a server error once the user changes any field, keeping the values. */
const ClearCreateErrorOnEdit = ({ hasError, reset }: { hasError: boolean; reset: () => void }) => {
  const { values } = useFormikContext<VirtualNetworkCreateFormValues>();
  // The error appears with the submitted values; only a later edit clears it.
  const submittedValues = useRef(JSON.stringify(values));
  useEffect(() => {
    const current = JSON.stringify(values);
    if (hasError && current !== submittedValues.current) {
      reset();
    }
    submittedValues.current = current;
  }, [values, hasError, reset]);
  return null;
};

/** The create form for a VirtualNetwork: project, name and IPv4 CIDR, in one step. */
export const VirtualNetworkCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { addToast } = useToast();
  const { mutateAsync: create, error, reset } = useCreateResource(VirtualNetworks);
  // A create that finishes after the user leaves must not navigate again.
  const leftPage = useRef(false);
  useEffect(() => {
    // StrictMode runs setup, cleanup, setup: setup must restore the flag or a
    // later success would be swallowed.
    leftPage.current = false;
    return () => {
      leftPage.current = true;
    };
  }, []);

  const validationSchema = useMemo(
    () =>
      Yup.object({
        metadata: Yup.object({
          name: Yup.string().required(t('Name is required')),
        }),
        ipv4Cidr: buildCidrSchema(t, 'ipv4').required(t('IPv4 CIDR is required')),
      }),
    [t],
  );

  const cancel = () => {
    leftPage.current = true;
    navigate('/networking/virtual-networks');
  };

  return (
    <ListPage
      title={t('Create virtual network')}
      description={t('Networks for workloads and catalog networking.')}
    >
      <PageSection hasBodyWrapper={false}>
        <Formik<VirtualNetworkCreateFormValues>
          initialValues={{ metadata: { name: '', project: '' }, ipv4Cidr: '' }}
          validationSchema={validationSchema}
          onSubmit={async (values) => {
            let created: VirtualNetwork | undefined;
            try {
              const resp = await create({
                object: {
                  metadata: values.metadata,
                  spec: { ipv4Cidr: values.ipv4Cidr || undefined },
                },
              });
              created = resp.object;
            } catch {
              // The mutation's error renders inline; the entered values stay.
              return;
            }
            if (leftPage.current) {
              return;
            }
            const name = created?.metadata?.name || created?.id || values.metadata.name;
            addToast({
              variant: 'success',
              title: t('Virtual network {{name}} created', { name }),
            });
            // Without an id in the reply the list is the only page to land on.
            navigate(
              created?.id
                ? `/networking/virtual-networks/${created.id}`
                : '/networking/virtual-networks',
            );
          }}
        >
          {({ submitForm, isSubmitting }) => (
            <Stack hasGutter>
              <ClearCreateErrorOnEdit hasError={Boolean(error)} reset={reset} />
              <StackItem>
                <div
                  onKeyDown={(event) => {
                    // Enter in a text field takes the same path as Create.
                    if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
                      event.preventDefault();
                      if (!isSubmitting) {
                        void submitForm();
                      }
                    }
                  }}
                >
                  <OsacForm>
                    <ProjectField />
                    <NameField />
                    <TextField
                      name="ipv4Cidr"
                      label={t('IPv4 CIDR')}
                      fieldId="vn-ipv4-cidr"
                      isRequired
                      placeholder="10.0.0.0/16"
                    />
                  </OsacForm>
                </div>
              </StackItem>
              {!!error && (
                <StackItem>
                  <Alert variant="danger" title={t('Failed to create virtual network')} isInline>
                    {getErrorMessage(error)}
                  </Alert>
                </StackItem>
              )}
              <StackItem>
                <Button variant="link" onClick={cancel}>
                  {t('Cancel')}
                </Button>
                <Button
                  variant="primary"
                  onClick={submitForm}
                  isDisabled={isSubmitting}
                  isLoading={isSubmitting}
                >
                  {t('Create')}
                </Button>
              </StackItem>
            </Stack>
          )}
        </Formik>
      </PageSection>
    </ListPage>
  );
};
