import {
  Card,
  CardBody,
  Grid,
  GridItem,
  PageSection,
  Skeleton,
  Stack,
  StackItem,
  Tab,
  TabContent,
  TabContentBody,
  TabTitleText,
  Tabs,
} from '@patternfly/react-core';

import { ResourceDetailHeaderSkeleton } from './ResourceDetailHeaderSkeleton';

interface ResourceDetailsPageLoadingProps {
  parentTo: string;
  parentLabel: string;
  tabLabels?: string[];
  tabsId?: string;
  cardCount?: number;
  sectionClassName?: string;
}

const tabContentId = (tabsId: string, key: number): string => `${tabsId}-content-${key}`;

const ResourceDetailCardSkeleton = () => (
  <Card isFullHeight>
    <CardBody>
      <Stack hasGutter>
        <StackItem>
          <Skeleton width="35%" fontSize="lg" />
        </StackItem>
        <StackItem>
          <Skeleton width="100%" />
        </StackItem>
        <StackItem>
          <Skeleton width="92%" />
        </StackItem>
        <StackItem>
          <Skeleton width="78%" />
        </StackItem>
        <StackItem>
          <Skeleton width="86%" />
        </StackItem>
      </Stack>
    </CardBody>
  </Card>
);

export const ResourceDetailsPageLoading = ({
  parentTo,
  parentLabel,
  tabLabels,
  tabsId = 'resource-detail-tabs-loading',
  cardCount = 2,
  sectionClassName,
}: ResourceDetailsPageLoadingProps) => {
  const cards = Array.from({ length: cardCount }, (_, index) => index);
  const gridMd = cardCount === 1 ? 12 : 6;
  const hasTabs = tabLabels !== undefined && tabLabels.length > 0;

  const cardGrid = (
    <Grid hasGutter>
      {cards.map((index) => (
        <GridItem key={index} md={gridMd}>
          <ResourceDetailCardSkeleton />
        </GridItem>
      ))}
    </Grid>
  );

  return (
    <>
      <PageSection hasBodyWrapper={false} className={sectionClassName}>
        <Stack hasGutter>
          <StackItem>
            <ResourceDetailHeaderSkeleton parentTo={parentTo} parentLabel={parentLabel} />
          </StackItem>
          {hasTabs && (
            <StackItem>
              <Tabs activeKey={0} id={tabsId}>
                {tabLabels.map((label, index) => (
                  <Tab
                    key={label}
                    eventKey={index}
                    title={<TabTitleText>{label}</TabTitleText>}
                    tabContentId={tabContentId(tabsId, index)}
                  />
                ))}
              </Tabs>
            </StackItem>
          )}
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false} className={sectionClassName}>
        {hasTabs ? (
          <TabContent eventKey={0} id={tabContentId(tabsId, 0)} activeKey={0}>
            <TabContentBody>{cardGrid}</TabContentBody>
          </TabContent>
        ) : (
          cardGrid
        )}
      </PageSection>
    </>
  );
};
