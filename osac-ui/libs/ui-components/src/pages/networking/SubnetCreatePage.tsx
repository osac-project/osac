import { useMemo, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  ActionGroup,
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Button,
  Tooltip,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import * as Yup from 'yup';

import {
  type Subnet,
  SubnetState,
  Subnets,
  type VirtualNetwork,
  VirtualNetworks,
} from '@osac/types';
import { useCreateResource, useListAllResources } from '@osac/ui-components/api/use-resource';
import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  resourceDisplayName,
  tenantDefaultSubnetFilter,
  virtualNetworkScopeFilter,
} from '@osac/ui-components/api/v1/networking';
import TextField from '@osac/ui-components/components/Form/base/TextField';
import {
  FieldValidationProvider,
  useFieldValidation,
} from '@osac/ui-components/components/Form/FieldValidationContext';
import NameField from '@osac/ui-components/components/Form/NameField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import { SelectField } from '@osac/ui-components/components/Form/SelectField';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import { useToast } from '@osac/ui-components/components/Toast/useToast';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { subnetCidrExample } from '../../components/networking/subnet-cidr-example';
import {
  SUBNETS_LIST_PATH,
  VIRTUAL_NETWORK_FILTER_PARAM,
  subnetReturnPath,
  virtualNetworkDetailsPath,
} from '../../components/networking/subnet-list-utils';
import { virtualNetworkOptionLabel } from '../../components/networking/virtual-network-option-label';
import { isTenantDefaultVirtualNetwork } from '../../components/networking/virtual-network-readiness';
import { getErrorMessage } from '../../utils/error';
import {
  buildCidrSchema,
  hasSubnetOverlap,
  isSubnetWithinVN,
} from '../../validation/cidr-validation';

interface SubnetCreateFormValues {
  virtualNetworkId: string;
  metadata: {
    name: string;
  };
  ipv4Cidr: string;
}

interface SubnetCreateFormProps {
  networks: readonly VirtualNetwork[];
  lockedNetwork?: VirtualNetwork;
  returnPath: string;
}

const SubnetCreateForm = ({ networks, lockedNetwork, returnPath }: SubnetCreateFormProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { addToast } = useToast();
  const { mutateAsync: create, error: createError } = useCreateResource(Subnets);
  const [submitError, setSubmitError] = useState<unknown>(null);
  const [selectedNetworkId, setSelectedNetworkId] = useState(lockedNetwork?.id ?? '');

  const selectedNetwork = networks.find((network) => network.id === selectedNetworkId);

  const subnetsQuery = useListAllResources(Subnets, virtualNetworkScopeFilter(selectedNetworkId), {
    enabled: Boolean(selectedNetworkId),
  });
  const siblingItems = subnetsQuery.data?.items;
  // React Query can hand back the previous filter's result for one render after
  // the selected network changes. Trust the result only when its members belong
  // to this network; otherwise an example could name a block the previous
  // network's subnets already use, and the form would suggest an overlap.
  const siblingsKnown =
    Boolean(selectedNetworkId) &&
    subnetsQuery.isSuccess &&
    (siblingItems ?? []).every((subnet) => subnet.spec?.virtualNetwork?.id === selectedNetworkId);
  const siblingSubnets = useMemo<Subnet[]>(
    () => (siblingsKnown ? (siblingItems ?? []) : []),
    [siblingsKnown, siblingItems],
  );

  const gateApplies = Boolean(selectedNetwork && isTenantDefaultVirtualNetwork(selectedNetwork));
  const defaultSubnetQuery = useListAllResources(
    Subnets,
    tenantDefaultSubnetFilter(selectedNetworkId),
    { enabled: gateApplies },
  );
  const defaultSubnetReady = (defaultSubnetQuery.data?.items ?? []).some(
    (subnet) => subnet.status?.state === SubnetState.READY,
  );
  const createBlocked =
    gateApplies &&
    !defaultSubnetQuery.isLoading &&
    !defaultSubnetQuery.error &&
    !defaultSubnetReady;

  const parentCidr = selectedNetwork?.spec?.ipv4Cidr ?? '';
  const existingCidrs = useMemo(
    () =>
      siblingSubnets
        .map((subnet) => subnet.spec?.ipv4Cidr)
        .filter((cidr): cidr is string => Boolean(cidr)),
    [siblingSubnets],
  );
  const cidrExample = siblingsKnown ? subnetCidrExample(parentCidr, existingCidrs) : undefined;
  const cidrHelper =
    !parentCidr || !siblingsKnown
      ? undefined
      : cidrExample
        ? t('Must be inside {{cidr}}.', { cidr: parentCidr })
        : t('No free block is available inside {{cidr}}.', { cidr: parentCidr });

  const validationSchema = useMemo(
    () =>
      Yup.object({
        virtualNetworkId: Yup.string().required(t('Virtual network is required')),
        metadata: Yup.object({
          name: Yup.string().required(t('Name is required')),
        }),
        ipv4Cidr: buildCidrSchema(t, 'ipv4')
          .required(t('IPv4 CIDR is required'))
          .test('within-vn', t('CIDR must be within parent virtual network range'), (value) => {
            if (!value || !parentCidr) {
              return true;
            }
            return isSubnetWithinVN(value, parentCidr);
          })
          .test('no-overlap', function (value) {
            if (!value) {
              return true;
            }
            const overlappingSubnet = siblingSubnets.find((subnet) => {
              const existingCidr = subnet.spec?.ipv4Cidr;
              return existingCidr && hasSubnetOverlap(value, [existingCidr]);
            });
            if (overlappingSubnet) {
              return this.createError({
                message: t('CIDR overlaps with existing subnet "{{name}}" ({{cidr}})', {
                  name: overlappingSubnet.metadata?.name || overlappingSubnet.id,
                  cidr: overlappingSubnet.spec?.ipv4Cidr,
                }),
              });
            }
            return true;
          }),
      }),
    [t, parentCidr, siblingSubnets],
  );
  const error = submitError ?? createError;

  return (
    <FieldValidationProvider>
      <Formik<SubnetCreateFormValues>
        initialValues={{
          virtualNetworkId: lockedNetwork?.id ?? '',
          metadata: { name: '' },
          ipv4Cidr: '',
        }}
        validationSchema={validationSchema}
        onSubmit={async (values) => {
          setSubmitError(null);
          const network = networks.find((candidate) => candidate.id === values.virtualNetworkId);
          if (!network) {
            setSubmitError(new Error(t('Select a virtual network.')));
            return;
          }
          try {
            await create({
              object: {
                metadata: {
                  name: values.metadata.name,
                  project: network.metadata?.project ?? '',
                },
                spec: {
                  virtualNetwork: { id: network.id },
                  ipv4Cidr: values.ipv4Cidr,
                },
              },
            });
            addToast({
              variant: 'success',
              title: t('Subnet {{name}} created', { name: values.metadata.name }),
            });
            navigate(returnPath);
          } catch (submissionError) {
            setSubmitError(submissionError);
          }
        }}
      >
        {(formik) => (
          <SubnetCreateFormFields
            formik={formik}
            networks={networks}
            lockedNetwork={lockedNetwork}
            returnPath={returnPath}
            createBlocked={createBlocked}
            cidrExample={cidrExample}
            cidrHelper={cidrHelper}
            error={error}
            onNetworkChange={(networkId) => {
              setSelectedNetworkId(networkId);
              void formik.setFieldValue('virtualNetworkId', networkId);
            }}
          />
        )}
      </Formik>
    </FieldValidationProvider>
  );
};

interface SubnetCreateFormFieldsProps {
  formik: {
    isSubmitting: boolean;
    handleSubmit: () => void;
  };
  networks: readonly VirtualNetwork[];
  lockedNetwork?: VirtualNetwork;
  returnPath: string;
  createBlocked: boolean;
  cidrExample?: string;
  cidrHelper?: string;
  error: unknown;
  onNetworkChange: (networkId: string) => void;
}

const SubnetCreateFormFields = ({
  formik,
  networks,
  lockedNetwork,
  returnPath,
  createBlocked,
  cidrExample,
  cidrHelper,
  error,
  onNetworkChange,
}: SubnetCreateFormFieldsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { setShowErrors } = useFieldValidation();

  return (
    <OsacForm>
      <SelectField
        name="virtualNetworkId"
        label={t('Virtual network')}
        fieldId="subnet-virtual-network"
        isRequired
        isDisabled={Boolean(lockedNetwork)}
        options={networks.map((network) => ({
          value: network.id,
          label: virtualNetworkOptionLabel(network),
        }))}
        placeholder={t('Select a virtual network')}
        helperText={lockedNetwork ? undefined : t('Only ready virtual networks are listed.')}
        onSelect={(value) => onNetworkChange(String(value))}
      />
      <NameField />
      <TextField
        name="ipv4Cidr"
        label={t('IPv4 CIDR')}
        fieldId="subnet-ipv4-cidr"
        isRequired
        placeholder={cidrExample}
        helperText={cidrHelper}
      />
      {createBlocked ? (
        <Alert
          variant="info"
          isInline
          isPlain
          title={t('Available when the default subnet is Ready.')}
        />
      ) : null}
      {!!error && (
        <Alert variant="danger" title={t('Failed to create subnet')} isInline>
          {getErrorMessage(error)}
        </Alert>
      )}
      <ActionGroup>
        {createBlocked ? (
          <Tooltip content={t('Available when the default subnet is Ready.')}>
            <CreateButton service={Subnets} isDisabled isLoading={formik.isSubmitting}>
              {t('Create')}
            </CreateButton>
          </Tooltip>
        ) : (
          <CreateButton
            service={Subnets}
            isLoading={formik.isSubmitting}
            onClick={() => {
              setShowErrors(true);
              formik.handleSubmit();
            }}
          >
            {t('Create')}
          </CreateButton>
        )}
        <Button
          variant="link"
          isDisabled={formik.isSubmitting}
          onClick={() => navigate(returnPath)}
        >
          {t('Cancel')}
        </Button>
      </ActionGroup>
    </OsacForm>
  );
};

export const SubnetCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const requestedNetworkId = searchParams.get(VIRTUAL_NETWORK_FILTER_PARAM) ?? '';

  const { data, isLoading, error } = useListAllResources(
    VirtualNetworks,
    VIRTUAL_NETWORK_READY_LIST_FILTER,
  );
  const networks = useMemo(() => data?.items ?? [], [data?.items]);
  const lockedNetwork = requestedNetworkId
    ? networks.find((network) => network.id === requestedNetworkId)
    : undefined;
  const returnPath = subnetReturnPath(requestedNetworkId || undefined);

  return (
    <ListPage
      title={t('Create subnet')}
      description={t('An address range inside a virtual network for its workloads.')}
      breadcrumb={
        <Breadcrumb>
          <BreadcrumbItem>
            <Button variant="link" isInline onClick={() => navigate(SUBNETS_LIST_PATH)}>
              {t('Subnets')}
            </Button>
          </BreadcrumbItem>
          {lockedNetwork ? (
            <BreadcrumbItem>
              <Button
                variant="link"
                isInline
                onClick={() => navigate(virtualNetworkDetailsPath(lockedNetwork.id))}
              >
                {resourceDisplayName(lockedNetwork.metadata, lockedNetwork.id)}
              </Button>
            </BreadcrumbItem>
          ) : null}
          <BreadcrumbItem isActive>{t('Create subnet')}</BreadcrumbItem>
        </Breadcrumb>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <SubnetCreateForm
          networks={networks}
          lockedNetwork={lockedNetwork}
          returnPath={returnPath}
        />
      </ListPageBody>
    </ListPage>
  );
};
