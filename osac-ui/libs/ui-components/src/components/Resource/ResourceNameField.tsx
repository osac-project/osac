import { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Content, Flex, FlexItem } from '@patternfly/react-core';

import TruncatedText from '@osac/ui-components/components/Primitives/TruncatedText.tsx';

export interface ResourceNameFieldProps {
  resource: {
    id: string;
    metadata?: {
      name?: string;
    };
  };
  title?: string; // Used to override the name/id used as the title
  subTitle?: string | ReactNode;
  detailsUrl?: string;
  maxCharsDisplayed?: number;
}

const ResourceNameField = ({
  resource,
  title,
  subTitle,
  detailsUrl,
  maxCharsDisplayed = 0,
}: ResourceNameFieldProps) => {
  const titleValue = title ?? (resource.metadata?.name || resource.id);
  const renderContent = (content: string) =>
    maxCharsDisplayed > 0 ? (
      <TruncatedText content={content} maxCharsDisplayed={maxCharsDisplayed} />
    ) : (
      content
    );

  return (
    <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsSm' }}>
      <FlexItem className="pf-v6-u-font-size-md">
        {detailsUrl ? (
          <Link className="pf-v6-u-font-weight-bold" to={detailsUrl}>
            {renderContent(titleValue)}
          </Link>
        ) : (
          renderContent(titleValue)
        )}
      </FlexItem>
      {subTitle ? (
        <FlexItem>
          {typeof subTitle === 'string' ? (
            <Content component="small">{renderContent(subTitle)}</Content>
          ) : (
            subTitle
          )}
        </FlexItem>
      ) : null}
    </Flex>
  );
};

export default ResourceNameField;
