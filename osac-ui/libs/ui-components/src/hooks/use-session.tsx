import { createContext, useCallback, useContext, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Bullseye, Spinner } from '@patternfly/react-core';

import { Capabilities, ServiceTier } from '@osac/types';

import type { UserRole } from '../shellTypes';
import {
  type Contrast,
  type ResolvedContrast,
  type ResolvedTheme,
  type Theme,
  useTheme,
} from './use-theme';
import { useUserPreferences } from './use-user-preferences';
import { useTranslation } from './useTranslation';
import { useGetResource } from '../api/use-resource';
import QueryErrorState from '../components/Resource/QueryErrorState';

interface SessionContextValue {
  role: UserRole;
  username: string;
  tenantId: string;
  userTheme: Theme;
  resolvedTheme: ResolvedTheme;
  setUserTheme: (theme: Theme) => void;
  userContrast: Contrast;
  resolvedContrast: ResolvedContrast;
  setUserContrast: (contrast: Contrast) => void;
  projects: string[];
  setProjects: (projects: string[]) => void;
  enabledServices: ServiceTier[];
}

const SessionContext = createContext<SessionContextValue | null>(null);

interface SessionProviderProps {
  children: React.ReactNode;
  role: UserRole;
  username: string;
  tenantId: string;
}

export const PROJECT_FILTER_PARAM = 'project';

/** localStorage key prefix for the per-user persisted project filter selection. */
export const PROJECT_FILTER_STORAGE_PREFIX = 'osac/project-filter/';

export const getProjectFilterStorageKey = (username: string) =>
  `${PROJECT_FILTER_STORAGE_PREFIX}${username}`;

export const SessionProvider = ({ children, role, username, tenantId }: SessionProviderProps) => {
  const { t } = useTranslation();
  const themeProps = useTheme();

  const [searchParams] = useSearchParams();
  const param = searchParams.get(PROJECT_FILTER_PARAM);

  // Persist the selection per user so it is recovered on the next visit. An
  // explicit URL param wins over the stored value so shared/deep links behave.
  const [storedProjects, setStoredProjects] = useUserPreferences(
    getProjectFilterStorageKey(username),
  );

  const [projects, setProjectsState] = useState<string[]>(() => {
    const initial = param ?? storedProjects;
    return initial ? initial.split(',') : [];
  });

  const setProjects = useCallback(
    (next: string[]) => {
      setProjectsState(next);
      setStoredProjects(next.join(','));
    },
    [setStoredProjects],
  );

  const { data, isLoading, error, refetch } = useGetResource(
    Capabilities,
    {},
    {
      staleTime: Infinity,
      refetchInterval: false,
    },
  );

  if (isLoading) {
    return (
      <Bullseye>
        <Spinner size="xl" />
      </Bullseye>
    );
  }

  if (error) {
    return (
      <QueryErrorState
        error={error}
        mode="page"
        onRetry={() => void refetch()}
        title={t('Failed to fetch capabilities')}
      />
    );
  }

  return (
    <SessionContext.Provider
      value={{
        role,
        username,
        tenantId,
        projects,
        setProjects,
        enabledServices: data?.enabledServices || [],
        ...themeProps,
      }}
    >
      {children}
    </SessionContext.Provider>
  );
};

export const useSession = (): SessionContextValue => {
  const ctx = useContext(SessionContext);
  if (!ctx) {
    throw new Error('useSession must be used inside SessionProvider');
  }

  return ctx;
};
