import { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Content, Flex, FlexItem } from '@patternfly/react-core';

import './ResourceNameField.css';

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
}

const ResourceNameField = ({ resource, title, subTitle, detailsUrl }: ResourceNameFieldProps) => {
  const titleValue = title ?? (resource.metadata?.name || resource.id);
  // PatternFly 6 has no multi-line clamp utility, so the clamp lives in
  // ResourceNameField.css. `-webkit-line-clamp` paints a trailing ellipsis when
  // the content exceeds three lines.
  const renderContent = (content: string) => (
    <span className="resource-name-field__clamped pf-v6-u-text-break-word">{content}</span>
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
