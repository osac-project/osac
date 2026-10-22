import { type ReactNode, useMemo, useState } from 'react';
import {
  Label,
  SearchInput,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';
import {
  Table,
  Tbody,
  Td,
  type TdProps,
  Th,
  Thead,
  Tr,
  TreeRowWrapper,
} from '@patternfly/react-table';

import { type Project, Projects } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListAllResources } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { SubtleContent } from '@osac/ui-components/components/SubtleContent/SubtleContent';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter.ts';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ProjectActionsMenu from './ProjectActionsMenu';
import ProjectStatusLabel from './ProjectStatusLabel';
import { type ProjectTreeNode, buildProjectTree, getProjectName } from './utils';

const ProjectListPage = () => {
  const { t } = useTranslation();
  const { role, tenantId } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);

  const filter = cel<Project>((filter) =>
    filter.and(
      filter.field('metadata.tenant').notEquals('shared'),
      search
        ? filter.or(
            filter.field('metadata.name').contains(search),
            filter.field('spec.title').contains(search),
          )
        : undefined,
    ),
  );

  const { data: projects = [], isLoading, error } = useListAllResources(Projects, filter);
  const [expandedPaths, setExpandedPaths] = useState<string[]>(['']);

  const canCreate = role === 'tenant-admin';

  const projectTree = useMemo(() => buildProjectTree(projects), [projects]);

  const renderRows = (
    nodes: ProjectTreeNode[],
    level = 1,
    rowIndex = 0,
    isHidden = false,
  ): ReactNode[] => {
    let nextRowIndex = rowIndex;

    return nodes.flatMap((node, position) => {
      const { project, path, children } = node;
      const hasChildren = children.length > 0;
      const isExpanded = hasChildren && expandedPaths.includes(path);
      const currentRowIndex = nextRowIndex;
      const treeRowProps = {
        isExpanded,
        isHidden,
        'aria-level': level,
        'aria-posinset': position + 1,
        'aria-setsize': children.length,
        toggleAriaLabel: t(isExpanded ? 'Collapse project' : 'Expand project'),
      };
      const treeRow: NonNullable<TdProps['treeRow']> = {
        onCollapse: () =>
          setExpandedPaths((previous) =>
            isExpanded
              ? previous.filter((expandedPath) => expandedPath !== path)
              : [...previous, path],
          ),
        rowIndex: currentRowIndex,
        props: treeRowProps,
      };

      const childRows = hasChildren
        ? renderRows(children, level + 1, currentRowIndex + 1, isHidden || !isExpanded)
        : [];

      nextRowIndex += childRows.length + 1;

      return [
        <TreeRowWrapper key={project.id} row={{ props: treeRowProps }}>
          <Td dataLabel={t('Name')} treeRow={treeRow}>
            <Stack>
              <StackItem>
                <ResourceNameField
                  resource={project}
                  title={getProjectName(project, t)}
                  detailsUrl={`/projects/${project.id}`}
                />
              </StackItem>
              {level > 2 && (
                <StackItem>
                  <Label isCompact>{t('Nested')}</Label>
                </StackItem>
              )}
              {project.metadata?.description && (
                <StackItem>
                  <SubtleContent>{project.metadata.description}</SubtleContent>
                </StackItem>
              )}
            </Stack>
          </Td>
          <Td dataLabel={t('Status')}>
            <ProjectStatusLabel project={project} />
          </Td>
          <Td dataLabel={t('Created')}>
            <Timestamp value={project.metadata?.creationTimestamp} />
          </Td>
          <Td dataLabel={t('Actions')} isActionCell>
            <ProjectActionsMenu project={project} />
          </Td>
        </TreeRowWrapper>,
        ...childRows,
      ];
    });
  };

  return (
    <ListPage
      title={t('Projects')}
      description={t('Organize resources with hierarchical projects.')}
      error={error}
      actions={
        canCreate && !!tenantId ? (
          <CreateButton service={Projects} to="/projects/create">
            {t('Create project')}
          </CreateButton>
        ) : undefined
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Toolbar>
          <ToolbarContent>
            <ToolbarGroup>
              <ToolbarItem>
                <SearchInput
                  placeholder={t('Search projects by name…')}
                  value={search}
                  onChange={(_e, v) => setSearch(v)}
                  onClear={() => setSearch('')}
                  aria-label={t('Search projects')}
                />
              </ToolbarItem>
            </ToolbarGroup>
          </ToolbarContent>
        </Toolbar>
        {projects.length === 0 ? (
          <SubtleContent component="p">
            {search
              ? t('No projects match your search.')
              : t('No projects yet. Create one to get started.')}
          </SubtleContent>
        ) : (
          <Table aria-label={t('Projects')} variant="compact" isTreeTable>
            <Thead>
              <Tr>
                <Th>{t('Name')}</Th>
                <Th>{t('Status')}</Th>
                <Th>{t('Created')}</Th>
                <Th aria-label={t('Actions')} />
              </Tr>
            </Thead>
            <Tbody>{renderRows(projectTree)}</Tbody>
          </Table>
        )}
      </ListPageBody>
    </ListPage>
  );
};

export default ProjectListPage;
