import { access, mkdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

import { testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import { catalogTestRunId } from '../test_data';

export type ResizeOwnedResource =
  | 'catalog'
  | 'disk-image'
  | 'instance-type-base'
  | 'instance-type-target'
  | 'storage-tier'
  | 'tenant';

const ownershipDirectory = path.join(os.tmpdir(), 'osac-vmaas-resize-ownership');

export const resizeOwnershipFilePath = (resource: ResizeOwnedResource): string =>
  path.join(ownershipDirectory, `${resource}-${catalogTestRunId}.created`);

export const markResizeResourceOwned = async (resource: ResizeOwnedResource): Promise<void> => {
  await mkdir(ownershipDirectory, { recursive: true, mode: 0o700 });
  await writeFile(resizeOwnershipFilePath(resource), '', {
    encoding: 'utf8',
    flag: 'wx',
    mode: 0o600,
  });
};

export const markResizeOwnershipIfApplicable = async (
  projectName: string,
  applicableProjects: readonly string[],
  resource: ResizeOwnedResource,
  stepCategory: string,
): Promise<void> => {
  if (!applicableProjects.includes(projectName)) {
    return;
  }
  await testStep(stepCategory, 'Record resize prerequisite ownership', () =>
    markResizeResourceOwned(resource),
  );
};

export const isResizeResourceOwned = async (resource: ResizeOwnedResource): Promise<boolean> => {
  try {
    await access(resizeOwnershipFilePath(resource));
    return true;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return false;
    }
    throw error;
  }
};

export const removeResizeOwnershipMarker = async (resource: ResizeOwnedResource): Promise<void> => {
  await rm(resizeOwnershipFilePath(resource), { force: true });
};
