import { describe, expect, it } from 'vitest';

import { getCreateProjectPayload } from './payload';
import type { ProjectCreateValues } from './values';

interface ProjectCreateValueOverrides {
  metadata?: Partial<ProjectCreateValues['metadata']>;
  title?: string;
}

const baseValues = ({
  metadata: metadataOverrides,
  ...overrides
}: ProjectCreateValueOverrides = {}): ProjectCreateValues => ({
  metadata: { name: 'child-proj', project: '', description: 'desc', ...metadataOverrides },
  title: 'Child',
  ...overrides,
});

describe('getCreateProjectPayload', () => {
  it('creates a root project with leaf name and empty project', () => {
    expect(getCreateProjectPayload(baseValues())).toEqual({
      metadata: { name: 'child-proj', project: '', description: 'desc' },
      spec: { title: 'Child' },
    });
  });

  it('encodes nested hierarchy in metadata.name and leaves project empty', () => {
    expect(
      getCreateProjectPayload(
        baseValues({ metadata: { name: 'nested-child-prj', project: 'nested-prj' } }),
      ),
    ).toEqual({
      metadata: { name: 'nested-prj.nested-child-prj', project: '', description: 'desc' },
      spec: { title: 'Child' },
    });
  });

  it('supports multi-level parents via full parent path', () => {
    expect(
      getCreateProjectPayload(baseValues({ metadata: { name: 'app', project: 'org.team' } })),
    ).toEqual({
      metadata: { name: 'org.team.app', project: '', description: 'desc' },
      spec: { title: 'Child' },
    });
  });

  it('treats a parent named "default" as a real nested parent (not the empty root)', () => {
    expect(
      getCreateProjectPayload(baseValues({ metadata: { name: 'my-project', project: 'default' } })),
    ).toEqual({
      metadata: { name: 'default.my-project', project: '', description: 'desc' },
      spec: { title: 'Child' },
    });
  });

  it('omits empty description', () => {
    expect(
      getCreateProjectPayload(
        baseValues({ metadata: { name: 'child-proj', project: '', description: '' } }),
      ),
    ).toEqual({
      metadata: { name: 'child-proj', project: '' },
      spec: { title: 'Child' },
    });
  });
});
