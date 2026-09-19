{{/*
Return the effective networking profile shared by the umbrella chart and its
subcharts. The public contract is global.networking; the returned map contains
the existing manager names and AAP selectors consumed by the charts.
*/}}
{{- define "osac.networking.effective" -}}
{{- $networking := .Values.global.networking | default dict -}}
{{- $provider := $networking.provider | default "none" -}}
{{- $overlay := $networking.overlay | default "k8s_only" -}}
{{- $netris := $networking.netris | default dict -}}
{{- $networkClass := $networking.networkClass | default dict -}}
{{- $fabricManager := "" -}}
{{- $k8sManager := "" -}}
{{- $aapNetworkClass := "" -}}
{{- $aapNetworkSteps := "" -}}
{{- if eq $provider "netris" -}}
  {{- if ne $overlay "none" -}}
    {{- fail (printf "global.networking: provider %q currently supports only overlay %q (got %q)" $provider "none" $overlay) -}}
  {{- end -}}
  {{- $fabricManager = "netris" -}}
  {{- $aapNetworkClass = "netris" -}}
  {{- $aapNetworkSteps = "netris.steps" -}}
{{- else if eq $provider "cudn" -}}
  {{- fail "global.networking: provider \"cudn\" is reserved but has no registered facade implementation yet" -}}
{{- else if eq $provider "none" -}}
  {{- if eq $overlay "k8s_only" -}}
    {{- $k8sManager = "k8s_only" -}}
    {{- $aapNetworkClass = "agentless_net" -}}
    {{- $aapNetworkSteps = "agentless_net.steps" -}}
  {{- else if ne $overlay "none" -}}
    {{- fail (printf "global.networking: provider %q currently supports overlays %q and %q (got %q)" $provider "none" "k8s_only" $overlay) -}}
  {{- end -}}
{{- else if eq $provider "vlan" -}}
  {{- fail "global.networking: provider \"vlan\" is reserved but has no registered implementation yet" -}}
{{- else -}}
  {{- fail (printf "global.networking.provider must be one of netris, vlan, cudn, or none (got %q)" $provider) -}}
{{- end -}}
{{- if not (has $overlay (list "none" "cudn_evpn" "k8s_only" "cudn_localnet")) -}}
  {{- fail (printf "global.networking.overlay must be one of none, cudn_evpn, k8s_only, or cudn_localnet (got %q)" $overlay) -}}
{{- end -}}
{{- $defaultTitle := "CUDN Network Implementation" -}}
{{- $defaultDescription := "Provisions networking resources using ClusterUserDefinedNetwork (CUDN) on OpenShift." -}}
{{- if eq $provider "netris" -}}
  {{- $defaultTitle = "Netris Network Implementation" -}}
  {{- $defaultDescription = "Provisions networking resources using Netris Controller API." -}}
{{- else if and (eq $provider "none") (eq $overlay "k8s_only") -}}
  {{- $defaultTitle = "K8s-only networking" -}}
  {{- $defaultDescription = "Provides Kubernetes-native networking without a separate physical fabric." -}}
{{- end -}}
{{- $defaultNetworkClass := dict
  "enabled" true
  "title" $defaultTitle
  "description" $defaultDescription
  "fabricManager" $fabricManager
  "k8sManager" $k8sManager
  "isDefault" true
  "defaults" (dict
    "virtualNetworkIPv4CIDR" "10.200.0.0/16"
    "subnetIPv4CIDR" "10.200.0.0/20"
    "enableNatGateway" false
    "egressRules" (list (dict "protocol" "PROTOCOL_ALL" "ipv4Cidr" "0.0.0.0/0"))
  )
-}}
{{- $effectiveNetworkClass := mergeOverwrite (deepCopy $defaultNetworkClass) $networkClass -}}
{{- $effectiveDefaults := mergeOverwrite (deepCopy $defaultNetworkClass.defaults) ($networkClass.defaults | default dict) -}}
{{- $_ := set $effectiveNetworkClass "defaults" $effectiveDefaults -}}
{{- if and (eq $provider "none") (eq $overlay "none") (eq ($effectiveNetworkClass.fabricManager | default "") "") (eq ($effectiveNetworkClass.k8sManager | default "") "") -}}
  {{- fail "global.networking: provider=none and overlay=none require an explicit networkClass manager" -}}
{{- end -}}
{{- if and (eq ($effectiveNetworkClass.k8sManager | default "") "k8s_only") (ne ($effectiveNetworkClass.fabricManager | default "") "") -}}
  {{- fail "global.networking.networkClass.k8sManager=k8s_only requires networkClass.fabricManager to be empty" -}}
{{- end -}}
{{- if eq $provider "netris" -}}
  {{- if ne ($effectiveNetworkClass.fabricManager | default "") "netris" -}}
    {{- fail (printf "global.networking.networkClass.fabricManager must be netris for provider netris (got %q)" ($effectiveNetworkClass.fabricManager | default "")) -}}
  {{- end -}}
{{- end -}}
{{- if eq $provider "cudn" -}}
  {{- if ne ($effectiveNetworkClass.fabricManager | default "") "cudn_net" -}}
    {{- fail (printf "global.networking.networkClass.fabricManager must be cudn_net for provider cudn (got %q)" ($effectiveNetworkClass.fabricManager | default "")) -}}
  {{- end -}}
{{- end -}}
{{- dict
  "provider" $provider
  "overlay" $overlay
  "netris" $netris
  "fabricManager" ($effectiveNetworkClass.fabricManager | default "")
  "k8sManager" ($effectiveNetworkClass.k8sManager | default "")
  "networkClass" $effectiveNetworkClass
  "aap" (dict "networkClass" $aapNetworkClass "networkStepsCollection" $aapNetworkSteps)
  | toYaml
-}}
{{- end -}}
