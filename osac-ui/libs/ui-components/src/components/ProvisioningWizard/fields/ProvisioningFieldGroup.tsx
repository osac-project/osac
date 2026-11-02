import type { ReactNode } from 'react';
import { Flex, FlexItem, FormSection, Title } from '@patternfly/react-core';

interface ProvisioningFieldGroupProps {
  title: string;
  titleId: string;
  actions?: ReactNode;
  children: ReactNode;
}

const ProvisioningFieldGroup = ({
  title,
  titleId,
  actions,
  children,
}: ProvisioningFieldGroupProps) => (
  <>
    <FormSection
      aria-labelledby={titleId}
      title={
        <Flex
          alignItems={{ default: 'alignItemsCenter' }}
          justifyContent={{ default: 'justifyContentSpaceBetween' }}
        >
          <FlexItem>
            <Title headingLevel="h2" size="md" id={titleId}>
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
