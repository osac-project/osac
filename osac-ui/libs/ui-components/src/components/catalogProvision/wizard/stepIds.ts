import { TFunction } from 'i18next';

import { CatalogProvisionKind } from '../catalogFieldDefinition';

// Full set of valid step ids (used for type + membership guard); not every kind uses all of them.
export const WIZARD_STEP_IDS = [
  'catalog',
  'general',
  'configuration',
  'storage',
  'networking',
  'access',
  'review',
];

export type WizardStepId = (typeof WIZARD_STEP_IDS)[number];

export const STEP_LABEL_KEYS = (t: TFunction): Record<WizardStepId, string> => ({
  catalog: t('Catalog item'),
  general: t('General'),
  configuration: t('Configuration'),
  storage: t('Storage'),
  networking: t('Networking'),
  access: t('Access & initial run'),
  review: t('Review'),
});

// Default/cluster order — no Storage step (that step is compute-instance only).
const CLUSTER_WIZARD_STEPS: readonly WizardStepId[] = [
  'catalog',
  'general',
  'configuration',
  'networking',
  'review',
];

const BARE_METAL_WIZARD_STEPS: readonly WizardStepId[] = [
  'catalog',
  'general',
  'configuration',
  'networking',
  'review',
];

// Compute instances get a dedicated Storage step between Configuration and Networking.
// Other kinds (cluster, bare metal) deliberately omit it.
const COMPUTE_INSTANCE_WIZARD_STEPS: readonly WizardStepId[] = [
  'catalog',
  'general',
  'configuration',
  'storage',
  'networking',
  'access',
  'review',
];

export const getWizardOrderedSteps = (kind?: CatalogProvisionKind): readonly WizardStepId[] => {
  if (kind === 'bare_metal_instance') {
    return BARE_METAL_WIZARD_STEPS;
  }
  if (kind === 'compute_instance') {
    return COMPUTE_INSTANCE_WIZARD_STEPS;
  }
  return CLUSTER_WIZARD_STEPS;
};
