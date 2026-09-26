{{/*
Expand global.networking into the effective NetworkClass fields and AAP knobs.

Public API is manager-first:
  fabricManager: "" | netris | cudn_net (CaaS/ci.steps only; vlan reserved)
  k8sManager: "" | k8s_only

Defaults when keys are omitted: fabricManager "" + k8sManager k8s_only (agentless).
Setting fabricManager to netris without an explicit k8sManager leaves k8sManager empty.

Validated combinations:
  - fabricManager=netris + k8sManager="" → netris AAP + NetworkClass fabricManager=netris
  - fabricManager=cudn_net + k8sManager="" → CaaS ci.steps + CUDN NetworkClass
  - fabricManager="" + k8sManager=k8s_only → agentless AAP + NetworkClass k8sManager=k8s_only
  - fabricManager="" + k8sManager="" → expert empty profile; networkClass must supply a manager

Rejected combinations:
  - any use of removed keys provider / overlay
  - both fabricManager and k8sManager set (non-empty)
  - fabricManager netris without a netris config block
  - fabricManager vlan (reserved); cudn_net without CaaS/ci.steps is rejected by validateValues
  - unknown fabricManager / k8sManager values
  - empty profile with no manager on the effective NetworkClass

Returns a dict with:
  fabricManager, k8sManager, aapNetrisEnabled, aapAgentlessEnabled, netris,
  networkClass, aap
*/}}
{{- define "osac.networking.effective" -}}
{{- $networking := ((.Values.global).networking) | default dict -}}

{{- if or (hasKey $networking "provider") (hasKey $networking "overlay") -}}
  {{- fail "global.networking.provider and global.networking.overlay were removed; set fabricManager and k8sManager instead (see docs/network-backend.md)" -}}
{{- end -}}

{{- $fabricManager := $networking.fabricManager | default "" -}}
{{- $k8sManager := "" -}}
{{- if hasKey $networking "k8sManager" -}}
  {{- $k8sManager = $networking.k8sManager | default "" -}}
{{- else if eq $fabricManager "" -}}
  {{- $k8sManager = "k8s_only" -}}
{{- end -}}

{{- $allowedFabric := list "" "netris" "cudn_net" -}}
{{- $reservedFabric := list "vlan" -}}
{{- $allowedK8s := list "" "k8s_only" -}}

{{- if has $fabricManager $reservedFabric -}}
  {{- fail (printf "global.networking.fabricManager %q is reserved and not yet supported" $fabricManager) -}}
{{- end -}}
{{- if not (has $fabricManager $allowedFabric) -}}
  {{- fail (printf "global.networking.fabricManager must be \"\", netris, or cudn_net (got %q)" $fabricManager) -}}
{{- end -}}
{{- if not (has $k8sManager $allowedK8s) -}}
  {{- fail (printf "global.networking.k8sManager must be \"\" or k8s_only (got %q)" $k8sManager) -}}
{{- end -}}
{{- if and (ne $fabricManager "") (ne $k8sManager "") -}}
  {{- fail (printf "global.networking cannot set both fabricManager=%q and k8sManager=%q; choose one manager" $fabricManager $k8sManager) -}}
{{- end -}}
{{- if and (eq $fabricManager "netris") (not $networking.netris) -}}
  {{- fail "global.networking.fabricManager=netris requires global.networking.netris configuration" -}}
{{- end -}}

{{- $aapNetrisEnabled := eq $fabricManager "netris" -}}
{{- $aapAgentlessEnabled := eq $k8sManager "k8s_only" -}}
{{- $aapNetworkClass := "" -}}
{{- $aapNetworkSteps := "" -}}
{{- if $aapNetrisEnabled -}}
  {{- $aapNetworkClass = "netris" -}}
  {{- $aapNetworkSteps = "netris.steps" -}}
{{- else if eq $fabricManager "cudn_net" -}}
  {{- $aapNetworkClass = "ci" -}}
  {{- $aapNetworkSteps = "ci.steps" -}}
{{- else if $aapAgentlessEnabled -}}
  {{- $aapNetworkSteps = "agentless_net.steps" -}}
{{- end -}}

{{- $defaultTitle := "K8s-only networking" -}}
{{- $defaultDescription := "Provides Kubernetes-native networking without a separate physical fabric." -}}
{{- if eq $fabricManager "netris" -}}
  {{- $defaultTitle = "Netris Network Implementation" -}}
  {{- $defaultDescription = "Provisions networking resources using Netris Controller API." -}}
{{- else if eq $fabricManager "cudn_net" -}}
  {{- $defaultTitle = "CUDN Network Implementation" -}}
  {{- $defaultDescription = "CUDN overlay for virtual bare-metal CaaS." -}}
{{- else if and (eq $fabricManager "") (eq $k8sManager "") -}}
  {{- $defaultTitle = "Custom Network Implementation" -}}
  {{- $defaultDescription = "NetworkClass managers are supplied via networkClass overrides." -}}
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

{{- $networkClassOverride := $networking.networkClass | default dict -}}
{{- $effectiveNetworkClass := mergeOverwrite (deepCopy $defaultNetworkClass) (deepCopy $networkClassOverride) -}}
{{- $effectiveDefaults := mergeOverwrite (deepCopy $defaultNetworkClass.defaults) ($networkClassOverride.defaults | default dict) -}}
{{- $_ := set $effectiveNetworkClass "defaults" $effectiveDefaults -}}

{{- $effectiveFabric := $effectiveNetworkClass.fabricManager | default "" -}}
{{- $effectiveK8s := $effectiveNetworkClass.k8sManager | default "" -}}
{{- if and (eq $effectiveFabric "") (eq $effectiveK8s "") -}}
  {{- fail "global.networking requires fabricManager, k8sManager, or networkClass overrides that set a manager" -}}
{{- end -}}
{{- if and (ne $effectiveFabric "") (ne $effectiveK8s "") -}}
  {{- fail (printf "effective NetworkClass cannot set both fabricManager=%q and k8sManager=%q" $effectiveFabric $effectiveK8s) -}}
{{- end -}}
{{- if has $effectiveFabric $reservedFabric -}}
  {{- fail (printf "NetworkClass fabricManager %q is reserved and not yet supported" $effectiveFabric) -}}
{{- end -}}
{{- if and (ne $effectiveFabric "") (not (has $effectiveFabric $allowedFabric)) -}}
  {{- fail (printf "NetworkClass fabricManager must be \"\", netris, or cudn_net (got %q)" $effectiveFabric) -}}
{{- end -}}
{{- if and (ne $effectiveK8s "") (not (has $effectiveK8s $allowedK8s)) -}}
  {{- fail (printf "NetworkClass k8sManager must be \"\" or k8s_only (got %q)" $effectiveK8s) -}}
{{- end -}}
{{- if and (ne $fabricManager "") (ne $effectiveFabric $fabricManager) -}}
  {{- fail (printf "global.networking.networkClass.fabricManager must be %s when fabricManager=%s (got %q)" $fabricManager $fabricManager $effectiveFabric) -}}
{{- end -}}
{{- if and (eq $effectiveFabric "cudn_net") (ne $fabricManager "cudn_net") -}}
  {{- fail "networkClass.fabricManager=cudn_net requires global.networking.fabricManager=cudn_net" -}}
{{- end -}}

{{- $netris := $networking.netris | default dict -}}
{{- if and $aapNetrisEnabled (not ($netris.controllerUrl | default "")) -}}
  {{- fail "global.networking.netris.controllerUrl is required when fabricManager=netris" -}}
{{- end -}}

{{- dict
      "fabricManager" $fabricManager
      "k8sManager" $k8sManager
      "aapNetrisEnabled" $aapNetrisEnabled
      "aapAgentlessEnabled" $aapAgentlessEnabled
      "netris" $netris
      "networkClass" $effectiveNetworkClass
      "aap" (dict "networkClass" $aapNetworkClass "networkStepsCollection" $aapNetworkSteps)
    | toYaml -}}
{{- end -}}
