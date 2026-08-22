# OSAC Chart Values Pattern

This document describes the pattern for transforming high-level configuration into detailed subchart values.

## Overview

The OSAC parent chart supports both:
1. **High-level properties** for convenience (e.g., `global.profilesList`)
2. **Detailed subchart values** for fine-grained control (e.g., `operator.controllers.clusterOrder`)

Subcharts contain template helpers that compute their configuration from high-level properties, while still allowing direct value overrides.

## Pattern

### Parent Chart (`osac-installer/charts/osac`)

**values.yaml**: Defines high-level properties under `global`:

```yaml
global:
  profilesList:
    - caas
    - vmaas
```

### Subchart (`osac-operator/charts/operator`)

**templates/_helpers.tpl**: Computes values from `global` with local override support:

```yaml
{{- define "osac-operator.controller.clusterOrder" -}}
{{- if .Values.controllers.clusterOrder | kindIs "invalid" | not -}}
{{- .Values.controllers.clusterOrder -}}
{{- else -}}
{{- $profiles := .Values.global.profilesList | default list -}}
{{- if or (has "caas" $profiles) (has "bmaas" $profiles) -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}
{{- end }}
```

**templates/deployment.yaml**: Uses the helper instead of reading `.Values` directly:

```yaml
- name: OSAC_ENABLE_CLUSTER_CONTROLLER
  value: {{ include "osac-operator.controller.clusterOrder" . | quote }}
```

## User Experience

### Scenario 1: High-level configuration (common case)

User sets service profiles:

```yaml
global:
  profilesList:
    - vmaas  # Enable vmaas only
```

**Result**: 
- `operator.controllers.computeInstance: true`
- `operator.controllers.clusterOrder: false`

### Scenario 2: Fine-grained override

User overrides a specific controller:

```yaml
global:
  profilesList:
    - vmaas

operator:
  controllers:
    clusterOrder: true  # Override: enable even though caas/bmaas not in profiles
```

**Result**:
- `operator.controllers.computeInstance: true` (computed from profilesList)
- `operator.controllers.clusterOrder: true` (explicit override)

## Implementation Checklist

When adding a new high-level property:

1. [ ] Add property to parent chart `values.yaml` under `global`
2. [ ] Document the property and which subchart values it affects
3. [ ] In affected subchart `_helpers.tpl`, create a helper that:
   - Checks if local value is explicitly set (`kindIs "invalid" | not`)
   - If set, uses local value
   - Otherwise, computes from `global` property
4. [ ] Update subchart templates to use the helper
5. [ ] Test both high-level and override scenarios

## Design Principles

1. **Computation lives with consumption**: Subcharts compute their own values from `global`, not the parent
2. **Overrides always win**: Explicit subchart values take precedence over computed values
3. **Backwards compatible**: Existing deployments with detailed values continue to work
4. **Clear relationships**: Document which high-level properties affect which subchart values
