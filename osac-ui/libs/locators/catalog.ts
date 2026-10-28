type CatalogLocator =
  | readonly [role: 'heading' | 'link', options: { name: string; exact: true }]
  | readonly [selector: string];

export const CatalogMainPage = {
  pageTitle: ['heading', { name: 'Catalog', exact: true }],
  catalogItemCards: ['.pf-v6-c-card'],
  catalogItemLabels: ['.pf-v6-c-card__header .pf-v6-c-label'],
  catalogItemResourceGroups: ['.pf-v6-c-description-list__group'],
  catalogItemResourceValue: ['.pf-v6-c-description-list__description'],
  catalogItemVisibility: ['.pf-v6-c-card__footer'],
} as const satisfies Record<string, CatalogLocator>;
