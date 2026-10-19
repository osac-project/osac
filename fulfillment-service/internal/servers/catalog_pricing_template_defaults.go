/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type catalogPricingTemplateDefaultsReader interface {
	templatePricingDefaults(context.Context, string) (map[string]string, error)
}

type catalogPricingTemplateReference struct {
	catalogItemID string
	templateID    string
}

func loadCatalogPricingTemplateDefaults(
	ctx context.Context,
	reader catalogPricingTemplateDefaultsReader,
	references []catalogPricingTemplateReference,
) (map[string]map[string]string, map[string]error) {
	defaultsByItem := make(map[string]map[string]string, len(references))
	errorsByItem := make(map[string]error)
	cache := make(map[string]map[string]string)
	errorsByTemplate := make(map[string]error)
	for _, reference := range references {
		if reference.catalogItemID == "" || reference.templateID == "" {
			errorsByItem[reference.catalogItemID] = errors.New("catalog item has no template identifier")
			continue
		}
		if defaults, ok := cache[reference.templateID]; ok {
			defaultsByItem[reference.catalogItemID] = defaults
			continue
		}
		if err, ok := errorsByTemplate[reference.templateID]; ok {
			errorsByItem[reference.catalogItemID] = err
			continue
		}
		if reader == nil {
			err := errors.New("catalog template defaults reader is unavailable")
			errorsByTemplate[reference.templateID] = err
			errorsByItem[reference.catalogItemID] = err
			continue
		}
		defaults, err := reader.templatePricingDefaults(ctx, reference.templateID)
		if err != nil {
			errorsByTemplate[reference.templateID] = err
			errorsByItem[reference.catalogItemID] = err
			continue
		}
		cache[reference.templateID] = defaults
		defaultsByItem[reference.catalogItemID] = defaults
	}
	return defaultsByItem, errorsByItem
}

func (s *ComputeInstanceCatalogItemsServer) templatePricingDefaults(ctx context.Context, templateID string) (map[string]string, error) {
	if templateID == "" || s.delegate == nil || s.delegate.templatesDao == nil {
		return nil, errors.New("compute instance catalog template is unavailable")
	}
	response, err := s.delegate.templatesDao.Get().SetId(templateID).Do(ctx)
	if err != nil {
		return nil, err
	}
	if response == nil || response.GetObject() == nil {
		return nil, errors.New("compute instance catalog template was not found")
	}
	return computeInstanceTemplatePricingDimensions(response.GetObject()), nil
}

func (s *ClusterCatalogItemsServer) templatePricingDefaults(ctx context.Context, templateID string) (map[string]string, error) {
	if templateID == "" || s.delegate == nil || s.delegate.templatesDao == nil {
		return nil, errors.New("cluster catalog template is unavailable")
	}
	response, err := s.delegate.templatesDao.Get().SetId(templateID).Do(ctx)
	if err != nil {
		return nil, err
	}
	if response == nil || response.GetObject() == nil {
		return nil, errors.New("cluster catalog template was not found")
	}
	return clusterTemplatePricingDimensions(response.GetObject()), nil
}

func computeInstanceTemplatePricingDimensions(template *privatev1.ComputeInstanceTemplate) map[string]string {
	if template == nil || template.GetSpecDefaults() == nil {
		return nil
	}
	return computeTemplatePricingDimensions(template.GetSpecDefaults().GetInstanceType().GetName())
}

func clusterTemplatePricingDimensions(template *privatev1.ClusterTemplate) map[string]string {
	if template == nil || template.GetSpecDefaults() == nil {
		return nil
	}
	return clusterTemplatePricingDimensionsFromVersion(template.GetSpecDefaults().GetVersion().GetName())
}

func computeTemplatePricingDimensions(instanceType string) map[string]string {
	if instanceType == "" {
		return nil
	}
	return map[string]string{"instance_type": instanceType}
}

func clusterTemplatePricingDimensionsFromVersion(version string) map[string]string {
	if version == "" {
		return nil
	}
	return map[string]string{"release_image": version}
}

func catalogPricingTemplateReferencesFromComputeItems(items []*publicv1.ComputeInstanceCatalogItem) []catalogPricingTemplateReference {
	references := make([]catalogPricingTemplateReference, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		references = append(references, catalogPricingTemplateReference{catalogItemID: item.GetId(), templateID: item.GetTemplate().GetId()})
	}
	return references
}

func catalogPricingTemplateReferencesFromClusterItems(items []*publicv1.ClusterCatalogItem) []catalogPricingTemplateReference {
	references := make([]catalogPricingTemplateReference, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		references = append(references, catalogPricingTemplateReference{catalogItemID: item.GetId(), templateID: item.GetTemplate().GetId()})
	}
	return references
}

func templateDefaultsReaderFrom(value any) catalogPricingTemplateDefaultsReader {
	reader, _ := value.(catalogPricingTemplateDefaultsReader)
	return reader
}

func catalogPricingDefaultsForEstimate(ctx context.Context, reader any, templateID string) (map[string]string, error) {
	defaultsReader := templateDefaultsReaderFrom(reader)
	if defaultsReader == nil {
		return nil, errors.New("catalog template defaults reader is unavailable")
	}
	return defaultsReader.templatePricingDefaults(ctx, templateID)
}

func computeCatalogPricingRequestsForServer(
	ctx context.Context,
	server *ComputeInstanceCatalogItemsServer,
	items []*publicv1.ComputeInstanceCatalogItem,
) []CatalogPricingRequest {
	requests := computeCatalogPricingRequests(items)
	if server == nil || server.pricingResolver == nil || catalogPricingEnrichmentDisabled(ctx) {
		return requests
	}
	defaults, failures := loadCatalogPricingTemplateDefaults(ctx, server, catalogPricingTemplateReferencesFromComputeItems(items))
	requests = computeCatalogPricingRequestsWithTemplateDefaults(items, defaults)
	for i, item := range items {
		if item != nil && failures[item.GetId()] != nil {
			requests[i].Supported = false
		}
	}
	return requests
}

func clusterCatalogPricingRequestsForServer(
	ctx context.Context,
	server *ClusterCatalogItemsServer,
	items []*publicv1.ClusterCatalogItem,
) []CatalogPricingRequest {
	requests := clusterCatalogPricingRequests(items)
	if server == nil || server.pricingResolver == nil || catalogPricingEnrichmentDisabled(ctx) {
		return requests
	}
	defaults, failures := loadCatalogPricingTemplateDefaults(ctx, server, catalogPricingTemplateReferencesFromClusterItems(items))
	requests = clusterCatalogPricingRequestsWithTemplateDefaults(items, defaults)
	for i, item := range items {
		if item != nil && failures[item.GetId()] != nil {
			requests[i].Supported = false
		}
	}
	return requests
}
