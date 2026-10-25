import type { ReactNode } from 'react';
import { Content, Divider, Flex, FlexItem, FormSection, Title } from '@patternfly/react-core';

interface ProvisioningFieldGroupProps {
  title: string;
  titleId: string;
  actions?: ReactNode;
  description?: ReactNode;
  hasDivider?: boolean;
  children: ReactNode;
}

const ProvisioningFieldGroup = ({
  title,
  titleId,
  actions,
  description,
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
      {description ? <Content component="p">{description}</Content> : null}
      {children}
    </FormSection>
  </>
);

export default ProvisioningFieldGroup;
