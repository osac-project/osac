import {
  Button,
  FormFieldGroup,
  FormFieldGroupHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import { useTranslation } from '../../../../hooks/useTranslation';
import TextField from '../../../Form/base/TextField';
import { type BareMetalInstanceTypeFormValues, emptyNetworkPortValue } from '../values';

const BareMetalNetworkPortsField = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceTypeFormValues>();
  const networkPorts = values.spec.networkPorts;

  return (
    <FieldArray name="spec.networkPorts">
      {(helpers) => (
        <Stack hasGutter>
          {networkPorts.length === 0 && <StackItem>{t('No network ports added.')}</StackItem>}
          {networkPorts.map((_, index) => (
            <StackItem key={index}>
              <FormFieldGroup
                header={
                  <FormFieldGroupHeader
                    titleText={{
                      text: t('Network port {{number}}', { number: index + 1 }),
                      id: `baremetal-network-port-group-${index}`,
                    }}
                    actions={
                      <Button
                        variant="plain"
                        aria-label={t('Remove network port')}
                        onClick={() => helpers.remove(index)}
                        icon={<MinusCircleIcon />}
                      />
                    }
                  />
                }
              >
                <TextField
                  name={`spec.networkPorts.${index}.name`}
                  label={t('Name')}
                  fieldId={`baremetal-network-port-${index}-name`}
                  isRequired
                  placeholder={t('e.g. data-0, mgmt-0')}
                />
                <TextField
                  name={`spec.networkPorts.${index}.role`}
                  label={t('Role')}
                  fieldId={`baremetal-network-port-${index}-role`}
                  isRequired
                  placeholder={t('e.g. fabric, management, storage, lifecycle')}
                />
                <TextField
                  name={`spec.networkPorts.${index}.type`}
                  label={t('Type')}
                  fieldId={`baremetal-network-port-${index}-type`}
                  isRequired
                  placeholder={t('e.g. Ethernet, InfiniBand')}
                />
                <TextField
                  name={`spec.networkPorts.${index}.speed`}
                  label={t('Speed')}
                  fieldId={`baremetal-network-port-${index}-speed`}
                  isRequired
                  placeholder={t('e.g. 1Gbps, 10Gbps, 100Gbps')}
                />
              </FormFieldGroup>
            </StackItem>
          ))}
          <StackItem>
            <Button
              variant="link"
              icon={<PlusCircleIcon />}
              onClick={() => helpers.push(emptyNetworkPortValue())}
            >
              {t('Add network port')}
            </Button>
          </StackItem>
        </Stack>
      )}
    </FieldArray>
  );
};

export default BareMetalNetworkPortsField;
