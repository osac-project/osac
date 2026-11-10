import { ReactNode } from 'react';
import { Flex, FlexItem } from '@patternfly/react-core';

export interface OsacCardFooterRowProps {
  term: ReactNode;
  description?: ReactNode;
}

const OsacCardFooterRow = ({ term, description }: OsacCardFooterRowProps) => {
  return (
    <Flex
      flexWrap={{ default: 'nowrap' }}
      gap={{ default: 'gapLg' }}
      justifyContent={{ default: 'justifyContentSpaceBetween' }}
    >
      <FlexItem>{term}</FlexItem>
      <FlexItem>{description}</FlexItem>
    </Flex>
  );
};

export default OsacCardFooterRow;
