import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Gallery,
  GalleryItem,
  SearchInput,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { Secret, Secrets } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import {
  DEFAULT_VIEW_TYPE,
  ViewType,
  getViewTypePrefKey,
  isViewType,
} from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useUserPreferences } from '@osac/ui-components/hooks/use-user-preferences';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import SecretCard from './SecretCard.tsx';
import SecretDeleteModal from './SecretDeleteModal.tsx';
import SecretTable from './SecretTable.tsx';
import TypeFilter from './TypeFilter.tsx';
import {
  TYPE_FILTER_TO_ENUM,
  TYPE_PARAM,
  type TypeFilterValue,
  isTypeFilterValue,
} from './utils.ts';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';
import ProjectFilter from '../Page/ProjectFilter';
import CreateButton from '../Primitives/CreateButton.tsx';
import ViewSwitcher from '../Primitives/ViewSwitcher.tsx';
import { SubtleContent } from '../SubtleContent/SubtleContent';

const SECRETS_VIEW_KEY = 'secrets';

const SecretListPage = () => {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [deleteTarget, setDeleteTarget] = useState<Secret>();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [type, setType, clearTypeFilter] = useArrayPageFilter<TypeFilterValue>(
    TYPE_PARAM,
    isTypeFilterValue,
  );
  const projectFilter = useProjectFilterQuery<Secret>();
  const [viewTypePref] = useUserPreferences(getViewTypePrefKey(SECRETS_VIEW_KEY));
  const viewType: ViewType = isViewType(viewTypePref) ? viewTypePref : DEFAULT_VIEW_TYPE;

  const typeFilter = type.map((t) => TYPE_FILTER_TO_ENUM[t]);

  const { data, isLoading, error } = useListResource(Secrets, {
    filter: cel<Secret>((filter) =>
      filter.and(
        projectFilter,
        search ? filter.field('metadata.name').contains(search) : undefined,
        type.length
          ? filter.or(...typeFilter.map((f) => filter.field('type').equals(f)))
          : undefined,
      ),
    ),
  });

  const items = data?.items ?? [];

  const handleEdit = (secret: Secret) => navigate(`/secrets/${secret.id}/edit`);
  const handleDelete = (secret: Secret) => setDeleteTarget(secret);

  return (
    <>
      {deleteTarget && (
        <SecretDeleteModal
          secret={deleteTarget}
          onClose={() => setDeleteTarget(undefined)}
          onSuccess={() => setDeleteTarget(undefined)}
        />
      )}
      <ListPage
        title={t('Secrets')}
        description={t(
          'Store credentials for use at launch. Encrypted at rest in the platform vault.',
        )}
        actions={<CreateButton to="/secrets/create">{t('Create secret')}</CreateButton>}
        error={error}
      >
        <ListPageBody isLoading={isLoading} error={error}>
          <Toolbar>
            <ToolbarContent>
              <ToolbarGroup>
                <ToolbarItem>
                  <ProjectFilter />
                </ToolbarItem>
                <ToolbarItem>
                  <TypeFilter type={type} onClear={clearTypeFilter} onToggle={setType} />
                </ToolbarItem>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search secrets')}
                    value={search}
                    onChange={(_e, v) => setSearch(v)}
                    onClear={() => setSearch('')}
                    aria-label={t('Search secrets')}
                  />
                </ToolbarItem>
              </ToolbarGroup>
              <ToolbarGroup align={{ default: 'alignEnd' }}>
                <ToolbarItem>
                  <ViewSwitcher pageKey={SECRETS_VIEW_KEY} />
                </ToolbarItem>
              </ToolbarGroup>
            </ToolbarContent>
          </Toolbar>
          {!items.length ? (
            <SubtleContent component="p">
              {search || projectFilter || type.length
                ? t('No secrets match your search.')
                : t('No secrets yet. Create one to get started.')}
            </SubtleContent>
          ) : (
            <>
              <SubtleContent component="p">
                {t('{{count}} secret', { count: items.length })}
              </SubtleContent>
              {viewType === 'cards' ? (
                <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                  {items.map((secret) => (
                    <GalleryItem key={secret.id}>
                      <SecretCard
                        secret={secret}
                        onEdit={() => handleEdit(secret)}
                        onDelete={() => handleDelete(secret)}
                      />
                    </GalleryItem>
                  ))}
                </Gallery>
              ) : (
                <SecretTable items={items} onEdit={handleEdit} onDelete={handleDelete} />
              )}
            </>
          )}
        </ListPageBody>
      </ListPage>
    </>
  );
};

export default SecretListPage;
