import { describe, expect, it } from 'vitest';

import { getCreateProjectPayload } from './payload';
import type { ProjectCreateValues } from './values';

const baseValues = (overrides: Partial<ProjectCreateValues> = {}): ProjectCreateValues => ({
  metadata: { name: 'child-proj', project: '', ...overrides.metadata },
  title: 'Child',
  description: 'desc',
  ...overrides,
});

describe('getCreateProjectPayload', () => {
  it('creates a root project with leaf name and empty project', () => {
    expect(getCreateProjectPayload(baseValues())).toEqual({
      metadata: { name: 'child-proj', project: '' },
      spec: { title: 'Child', description: 'desc' },
    });
  });

  it('encodes nested hierarchy in metadata.name and leaves project empty', () => {
    expect(
      getCreateProjectPayload(
        baseValues({ metadata: { name: 'nested-child-prj', project: 'nested-prj' } }),
      ),
    ).toEqual({
      metadata: { name: 'nested-prj.nested-child-prj', project: '' },
      spec: { title: 'Child', description: 'desc' },
    });
  });

  it('supports multi-level parents via full parent path', () => {
    expect(
      getCreateProjectPayload(baseValues({ metadata: { name: 'app', project: 'org.team' } })),
    ).toEqual({
      metadata: { name: 'org.team.app', project: '' },
      spec: { title: 'Child', description: 'desc' },
    });
  });

  it('treats a parent named "default" as a real nested parent (not the empty root)', () => {
    expect(
      getCreateProjectPayload(baseValues({ metadata: { name: 'my-project', project: 'default' } })),
    ).toEqual({
      metadata: { name: 'default.my-project', project: '' },
      spec: { title: 'Child', description: 'desc' },
    });
  });

  it('omits empty description', () => {
    expect(getCreateProjectPayload(baseValues({ description: '' }))).toEqual({
      metadata: { name: 'child-proj', project: '' },
      spec: { title: 'Child' },
    });
  });
});
