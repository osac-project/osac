import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema } from '@osac/types';

import ClusterDetailsSummary from './ClusterDetailsSummary';
import ClusterLifecycleSection from './ClusterLifecycleSection';
import ClusterSettingsSection from './ClusterSettingsSection';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('Cluster property styling', () => {
  it('uses native compact PatternFly terms and descriptions without decorative typography', () => {
    const cluster = create(ClusterSchema, {
      metadata: { project: 'example-project' },
      spec: { sshKey: { id: 'secret-ssh-1', name: 'tenant-ssh-key' } },
    });
    renderWithProviders(
      <>
        <ClusterDetailsSummary cluster={cluster} />
        <ClusterLifecycleSection cluster={cluster} />
        <ClusterSettingsSection cluster={cluster} />
      </>,
    );

    for (const label of ['Cluster overview', 'Cluster lifecycle', 'Cluster settings']) {
      const list = screen.getByLabelText(label);
      expect(list.tagName).toBe('DL');
      expect(list).toHaveClass('pf-v6-c-description-list', 'pf-m-compact');
      for (const term of within(list).getAllByRole('term')) {
        expect(term).toHaveClass('pf-v6-c-description-list__term');
      }
      for (const description of within(list).getAllByRole('definition')) {
        expect(description).toHaveClass('pf-v6-c-description-list__description');
      }
      expect(
        list.querySelector(
          '[class*="pf-v6-u-font"], [class*="text-color"], strong, small, [style]',
        ),
      ).toBeNull();
    }
    const sshKey = screen.getByRole('link', { name: 'tenant-ssh-key' }).closest('dd');
    expect(sshKey).toHaveClass('cluster-details__ssh-key');
  });
});
