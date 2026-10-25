import type { ReactNode } from 'react';
import { Divider, Flex, FlexItem, FormSection, Title } from '@patternfly/react-core';

interface ProvisioningFieldGroupProps {
  title: string;
  titleId: string;
  actions?: ReactNode;
  hasDivider?: boolean;
  children: ReactNode;
}

const ProvisioningFieldGroup = ({
  title,
  titleId,
  actions,
  hasDivider = false,
  children,
}: ProvisioningFieldGroupProps) => (
  <>
    {hasDivider ? <Divider /> : null}
    <FormSection
      aria-labelledby={titleId}
      title={
        <Flex
          alignItems={{ default: 'alignItemsCenter' }}
          justifyContent={{ default: 'justifyContentSpaceBetween' }}
        >
          <FlexItem>
            <Title headingLevel="h3" size="md" id={titleId}>
              {title}
            </Title>
          </FlexItem>
          {actions ? <FlexItem>{actions}</FlexItem> : null}
        </Flex>
      }
    >
      {children}
    </FormSection>
  </>
);

export default ProvisioningFieldGroup;
