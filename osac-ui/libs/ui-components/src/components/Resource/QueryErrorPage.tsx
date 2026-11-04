import { type EmptyStateProps, PageSection } from '@patternfly/react-core';

import QueryErrorState, { type QueryErrorStateProps } from './QueryErrorState';

type QueryErrorPageProps = Omit<QueryErrorStateProps, 'mode'> & {
  headingLevel?: EmptyStateProps['headingLevel'];
  sectionClassName?: string;
};

const QueryErrorPage = ({
  headingLevel = 'h1',
  sectionClassName,
  ...props
}: QueryErrorPageProps) => (
  <PageSection hasBodyWrapper={false} isFilled className={sectionClassName}>
    <QueryErrorState {...props} mode="page" headingLevel={headingLevel} />
  </PageSection>
);

export default QueryErrorPage;
