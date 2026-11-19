import { describe, expect, it } from 'vitest';

import { RoleBindingState } from '@osac/types';

import { buildRoleBindingListFilter } from './role-binding-list-filter';

describe('buildRoleBindingListFilter', () => {
  it('returns undefined when no criteria are set', () => {
    expect(buildRoleBindingListFilter({})).toBeUndefined();
  });

  it('builds a name search filter', () => {
    const filter = buildRoleBindingListFilter({ search: 'admin' });
    expect(filter).toContain('this.metadata.name.contains("admin")');
    expect(filter).toContain('this.id.contains("admin")');
  });

  it('builds a status filter', () => {
    const filter = buildRoleBindingListFilter({ statuses: ['pending'] });
    expect(filter).toBe(`this.status.state in [${RoleBindingState.PENDING}]`);
  });

  it('builds a multi-select status filter', () => {
    const filter = buildRoleBindingListFilter({ statuses: ['ready', 'failed'] });
    expect(filter).toBe(
      `this.status.state in [${RoleBindingState.READY}, ${RoleBindingState.FAILED}]`,
    );
  });

  it('combines search and status filters', () => {
    const filter = buildRoleBindingListFilter({ search: 'viewer', statuses: ['ready'] });
    expect(filter).toContain(`this.status.state in [${RoleBindingState.READY}]`);
    expect(filter).toContain('this.metadata.name.contains("viewer")');
    expect(filter).toContain(' && ');
  });

  it('builds a role filter', () => {
    const filter = buildRoleBindingListFilter({ roleIds: ['role-viewer'] });
    expect(filter).toBe('this.spec.role.id in ["role-viewer"]');
  });

  it('builds a multi-select role filter', () => {
    const filter = buildRoleBindingListFilter({
      roleIds: ['role-viewer', 'role-editor'],
    });
    expect(filter).toBe('this.spec.role.id in ["role-viewer", "role-editor"]');
  });

  it('combines status and role filters', () => {
    const filter = buildRoleBindingListFilter({
      statuses: ['ready'],
      roleIds: ['role-viewer'],
    });
    expect(filter).toContain(`this.status.state in [${RoleBindingState.READY}]`);
    expect(filter).toContain('this.spec.role.id in ["role-viewer"]');
    expect(filter).toContain(' && ');
  });
});
