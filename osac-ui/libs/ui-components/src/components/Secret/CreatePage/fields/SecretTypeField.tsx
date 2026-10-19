import {
  Card,
  CardBody,
  CardTitle,
  Flex,
  FlexItem,
  FormGroup,
  Gallery,
  GalleryItem,
  HelperText,
  HelperTextItem,
  Label,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';
import type { TFunction } from 'i18next';

import { SecretType } from '@osac/types';

import { useTranslation } from '../../../../hooks/useTranslation';
import type { SecretValues } from '../values';

import './SecretTypeField.css';

interface SecretTypeOption {
  title: string;
  description: string;
}

const SECRET_TYPES = [
  SecretType.OPAQUE,
  SecretType.KUBECONFIG,
  SecretType.PULL_SECRET,
  SecretType.SSH_PUBLIC_KEY,
  SecretType.USER_DATA,
  SecretType.VALUE,
] as const;

const getSecretTypeOptions = (
  t: TFunction,
): Record<(typeof SECRET_TYPES)[number], SecretTypeOption> => ({
  [SecretType.PULL_SECRET]: {
    title: t('Image pull secret'),
    description: t('Registry credentials / .dockerconfigjson'),
  },
  [SecretType.KUBECONFIG]: {
    title: t('Kubeconfig'),
    description: t('Cluster access file (.kube/config)'),
  },
  [SecretType.OPAQUE]: {
    title: t('Opaque'),
    description: t('Arbitrary key/value pairs'),
  },
  [SecretType.VALUE]: {
    title: t('Single value'),
    description: t('One string (token, password, license key)'),
  },
  [SecretType.SSH_PUBLIC_KEY]: {
    title: t('SSH public key'),
    description: t('OpenSSH public key for VM access'),
  },
  [SecretType.USER_DATA]: {
    title: t('User data'),
    description: t('Cloud-init script or config at launch'),
  },
});

interface SecretTypeFieldProps {
  isEdit: boolean;
}

const SecretTypeField = ({ isEdit }: SecretTypeFieldProps) => {
  const { t } = useTranslation();
  const { values, setFieldValue } = useFormikContext<SecretValues>();
  const options = getSecretTypeOptions(t);

  return (
    <FormGroup label={t('Secret type')} fieldId="secret-type" isRequired>
      <Gallery hasGutter minWidths={{ default: '30%' }}>
        {SECRET_TYPES.map((type) => {
          const isSelected = values.type === type;
          const cardId = `secret-type-${type}`;
          const option = options[type];

          return (
            <GalleryItem key={type}>
              <Card
                className="secret-type-card"
                id={cardId}
                isCompact
                isFullHeight
                isSelectable
                isSelected={isSelected}
                isDisabled={isEdit}
                onClick={() => {
                  void setFieldValue('type', type);
                }}
              >
                <CardTitle>
                  <Flex
                    justifyContent={{ default: 'justifyContentSpaceBetween' }}
                    alignItems={{ default: 'alignItemsCenter' }}
                  >
                    <FlexItem>{option.title}</FlexItem>
                    {isSelected && (
                      <FlexItem>
                        <Label color="blue">{t('Selected')}</Label>
                      </FlexItem>
                    )}
                  </Flex>
                </CardTitle>
                <CardBody>{option.description}</CardBody>
              </Card>
            </GalleryItem>
          );
        })}
      </Gallery>
      {(values.type === SecretType.UNSPECIFIED || !values.type) && (
        <HelperText>
          <HelperTextItem>{t('Select a secret type to continue.')}</HelperTextItem>
        </HelperText>
      )}
    </FormGroup>
  );
};

export default SecretTypeField;
