import { expectOsacCommandSucceeded, runOsacCommand } from './cli';

export const catalogItemResource = 'computeinstancecatalogitems';

export interface CatalogItemReference {
  name?: string;
  shared?: boolean;
}

export interface CatalogItemFields {
  instance_type?: {
    editable?: {
      default_value?: CatalogItemReference;
    };
  };
  boot_disk?: {
    size_gib?: {
      editable?: {
        default_value?: number;
      };
    };
  };
  disk_image?: {
    locked?: CatalogItemReference;
    editable?: {
      default_value?: CatalogItemReference;
    };
  };
}

export interface CatalogItem {
  description?: string;
  fields?: CatalogItemFields;
  metadata?: {
    name?: string;
  };
  template?: {
    id?: string;
  };
  title?: string;
}

export const getCatalogItems = async (name: string): Promise<CatalogItem[]> => {
  const result = await runOsacCommand(['get', catalogItemResource, name, '--output', 'json']);
  expectOsacCommandSucceeded(result, `Get catalog item '${name}'`);

  const parsed: unknown = JSON.parse(result.stdout);
  if (Array.isArray(parsed)) {
    return parsed as CatalogItem[];
  }
  if (parsed !== null && typeof parsed === 'object') {
    return [parsed as CatalogItem];
  }

  throw new Error(
    `The CLI returned an unexpected JSON response while getting catalog item '${name}'.`,
  );
};
