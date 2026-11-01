{{/*
Networking helpers (unified networking design). values.schema.json checks each
field; osac.networking.validate checks the cross-field rules. The installer's
NetworkClass step (files/network-class-step.py) checks them again together
with the rules that need the fulfillment service.
*/}}

{{/* Enabled services that shape the address plan (MaaS adds nothing). */}}
{{- define "osac.networking.enabledServices" -}}
{{- $s := .Values.global.services -}}
{{- toJson (dict "vmaas" $s.vmaas.enabled "bmaas" $s.bmaas.enabled "caas" $s.caas.enabled) -}}
{{- end -}}

{{/*
The address plan in force: global.networking.networkClass.addressPlan when set,
otherwise the default plan derived from the enabled services and k8sManager.
Returns JSON {overlay, fabric, lbVip, explicit}.
*/}}
{{- define "osac.networking.addressPlan" -}}
{{- $networking := .Values.global.networking -}}
{{- $plan := ($networking.networkClass | default dict).addressPlan -}}
{{- if $plan -}}
{{- toJson (dict "overlay" $plan.overlay "fabric" $plan.fabric "lbVip" $plan.lbVip "explicit" true) -}}
{{- else -}}
{{- $services := include "osac.networking.enabledServices" . | fromJson -}}
{{- $derived := list 0.0 1.0 0.0 -}}
{{- if $networking.k8sManager -}}
  {{- if $services.caas -}}{{- $derived = list 0.5 0.25 0.25 -}}
  {{- else if $services.bmaas -}}{{- $derived = list 0.5 0.5 0.0 -}}
  {{- else -}}{{- $derived = list 0.75 0.25 0.0 -}}
  {{- end -}}
{{- else if $services.caas -}}
  {{- $derived = list 0.0 0.75 0.25 -}}
{{- end -}}
{{- toJson (dict "overlay" (index $derived 0) "fabric" (index $derived 1) "lbVip" (index $derived 2) "explicit" false) -}}
{{- end -}}
{{- end -}}

{{/*
Prefix limit of a plan: 28 - log2(1/g), where g is the largest power of two at
most 1 that divides every non-zero share. Shares are multiples of 1/16, so the
limit is 24 + log2 of the largest of 16, 8, 4, 2, 1 that divides every non-zero
share counted in sixteenths.
*/}}
{{- define "osac.networking.prefixLimit" -}}
{{- $sixteenths := list -}}
{{- range $share := list .overlay .fabric .lbVip -}}
  {{- $n := mulf $share 16 | int -}}
  {{- if gt $n 0 }}{{ $sixteenths = append $sixteenths $n }}{{ end -}}
{{- end -}}
{{- $limit := 24 -}}
{{- range $exp, $m := list 1 2 4 8 16 -}}
  {{- $divides := true -}}
  {{- range $sixteenths }}{{ if ne (mod . $m) 0 }}{{ $divides = false }}{{ end }}{{ end -}}
  {{- if $divides }}{{ $limit = add 24 $exp }}{{ end -}}
{{- end -}}
{{- $limit -}}
{{- end -}}

{{/*
Parse an IPv4 CIDR string. Returns JSON {address, prefix, canonical}: the
address as an integer, the prefix length, and whether the host bits are zero.
*/}}
{{- define "osac.networking.parseCidr" -}}
{{- $parts := splitList "/" . -}}
{{- $octets := splitList "." (first $parts) -}}
{{- $address := 0 -}}
{{- range $octets }}{{ $address = add (mul $address 256) (atoi .) }}{{ end -}}
{{- $prefix := atoi (last $parts) -}}
{{- $size := 1 -}}
{{- range until (sub 32 $prefix | int) }}{{ $size = mul $size 2 }}{{ end -}}
{{- toJson (dict "address" $address "prefix" $prefix "size" $size "canonical" (eq (mod $address $size) 0)) -}}
{{- end -}}

{{/* Fail the render when the networking values break a cross-field rule. */}}
{{- define "osac.networking.validate" -}}
{{- $networking := .Values.global.networking -}}
{{- $services := include "osac.networking.enabledServices" . | fromJson -}}
{{- $nc := $networking.networkClass | default dict -}}
{{- if not $networking.fabricManager -}}
  {{- fail "global.networking.fabricManager is required: set it to the name in the Fabric Manager registration" -}}
{{- end -}}
{{- if and $services.vmaas (not $networking.k8sManager) -}}
  {{- fail "global.networking.k8sManager is required when VMaaS is enabled (set cudn_evpn)" -}}
{{- end -}}
{{- if and $networking.provisioningEnabled (not .Values.aap.instanceGroups.networkFulfillment.enabled) -}}
  {{- fail "global.networking.provisioningEnabled requires aap.instanceGroups.networkFulfillment.enabled=true" -}}
{{- end -}}

{{- $planPath := "global.networking.networkClass.addressPlan" -}}
{{- if $nc.addressPlan -}}
  {{- $set := 0 -}}
  {{- range $share := list "overlay" "fabric" "lbVip" }}{{ if hasKey $nc.addressPlan $share }}{{ $set = add1 $set }}{{ end }}{{ end -}}
  {{- if ne $set 3 -}}
    {{- fail (printf "%s must set overlay, fabric, and lbVip together" $planPath) -}}
  {{- end -}}
{{- end -}}
{{- $plan := include "osac.networking.addressPlan" . | fromJson -}}
{{- $sum := 0.0 -}}
{{- range $share := list "overlay" "fabric" "lbVip" -}}
  {{- $v := index $plan $share -}}
  {{- $sixteenths := mulf $v 16 -}}
  {{- if or (ne $v $v) (lt $v 0.0) (gt $v 1.0) (ne $sixteenths (floor $sixteenths)) -}}
    {{- fail (printf "%s.%s must be a multiple of 0.0625 from 0 to 1 (got %v)" $planPath $share $v) -}}
  {{- end -}}
  {{- $sum = addf $sum $v -}}
{{- end -}}
{{- if ne $sum 1.0 -}}
  {{- fail (printf "%s shares must sum to exactly 1 (got %v)" $planPath $sum) -}}
{{- end -}}
{{- if ne (gt (float64 $plan.overlay) 0.0) (not (empty $networking.k8sManager)) -}}
  {{- fail (printf "%s.overlay must be greater than 0 exactly when global.networking.k8sManager is set" $planPath) -}}
{{- end -}}
{{- if and (or $services.bmaas $services.caas) (eq (float64 $plan.fabric) 0.0) -}}
  {{- fail (printf "%s.fabric must be greater than 0 when BMaaS or CaaS is enabled" $planPath) -}}
{{- end -}}
{{- if and $services.caas (eq (float64 $plan.lbVip) 0.0) -}}
  {{- fail (printf "%s.lbVip must be greater than 0 when CaaS is enabled" $planPath) -}}
{{- end -}}

{{- $defaultsPath := "global.networking.networkClass.defaults" -}}
{{- $defaults := $nc.defaults -}}
{{- $cidrs := dict -}}
{{- range $field := list "virtualNetworkIPv4CIDR" "subnetIPv4CIDR" -}}
  {{- $value := index $defaults $field -}}
  {{- $cidr := include "osac.networking.parseCidr" $value | fromJson -}}
  {{- if not $cidr.canonical -}}
    {{- fail (printf "%s.%s must be a canonical IPv4 CIDR (got %s)" $defaultsPath $field $value) -}}
  {{- end -}}
  {{- $_ := set $cidrs $field $cidr -}}
{{- end -}}
{{- $vn := $cidrs.virtualNetworkIPv4CIDR -}}
{{- $subnet := $cidrs.subnetIPv4CIDR -}}
{{- $subnetStart := int64 $subnet.address -}}
{{- if or (lt (int $subnet.prefix) (int $vn.prefix)) (ne (sub $subnetStart (mod $subnetStart (int64 $vn.size))) (int64 $vn.address)) -}}
  {{- fail (printf "%s.subnetIPv4CIDR must be inside virtualNetworkIPv4CIDR" $defaultsPath) -}}
{{- end -}}
{{- $limit := include "osac.networking.prefixLimit" $plan | int -}}
{{- if gt (int $subnet.prefix) $limit -}}
  {{- fail (printf "%s.subnetIPv4CIDR prefix /%d is longer than the address plan's prefix limit /%d" $defaultsPath (int $subnet.prefix) $limit) -}}
{{- end -}}
{{- range $direction := list "ingressRules" "egressRules" -}}
  {{- range $i, $rule := (index $defaults $direction | default list) -}}
    {{- if and (hasKey $rule "portFrom") (hasKey $rule "portTo") (gt (int $rule.portFrom) (int $rule.portTo)) -}}
      {{- fail (printf "%s.%s[%d]: portFrom (%v) must be <= portTo (%v)" $defaultsPath $direction $i $rule.portFrom $rule.portTo) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if and $networking.provisioningEnabled (eq ($networking.k8sManager | default "") "cudn_evpn") -}}
  {{- range $field := list "ingressRules" "egressRules" -}}
    {{- range $i, $rule := (index $defaults $field | default list) -}}
      {{- if eq $rule.protocol "PROTOCOL_ICMP" -}}
        {{- fail (printf "%s.%s[%d]: PROTOCOL_ICMP needs the security_group_icmp capability, which cudn_evpn does not declare; use PROTOCOL_ALL" $defaultsPath $field $i) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/* SecurityGroup rules in the private API's JSON shape. */}}
{{- define "osac.networking.apiRules" -}}
{{- $rules := list -}}
{{- range . -}}
  {{- $rule := dict "protocol" .protocol "ipv4_cidr" .ipv4Cidr -}}
  {{- if hasKey . "portFrom" }}{{ $_ := set $rule "port_from" .portFrom }}{{ end -}}
  {{- if hasKey . "portTo" }}{{ $_ := set $rule "port_to" .portTo }}{{ end -}}
  {{- $rules = append $rules $rule -}}
{{- end -}}
{{- toJson $rules -}}
{{- end -}}

{{/* Input of the installer's NetworkClass step (files/network-class-step.py). */}}
{{- define "osac.networking.stepInput" -}}
{{- $networking := .Values.global.networking -}}
{{- $nc := $networking.networkClass -}}
{{- $plan := include "osac.networking.addressPlan" . | fromJson -}}
{{- $ew := $nc.eastWestCapabilities | default dict -}}
{{- $input := dict
  "fabric_manager" $networking.fabricManager
  "k8s_manager" ($networking.k8sManager | default "")
  "defaults" (dict
    "virtual_network_ipv4_cidr" $nc.defaults.virtualNetworkIPv4CIDR
    "subnet_ipv4_cidr" $nc.defaults.subnetIPv4CIDR
    "ingress_rules" (include "osac.networking.apiRules" ($nc.defaults.ingressRules | default list) | fromJsonArray)
    "egress_rules" (include "osac.networking.apiRules" ($nc.defaults.egressRules | default list) | fromJsonArray))
  "east_west_capabilities" (dict "supports_east_west_ethernet" ($ew.supportsEastWestEthernet | default false))
  "address_plan" (dict "overlay" $plan.overlay "fabric" $plan.fabric "lb_vip" $plan.lbVip)
  "address_plan_explicit" $plan.explicit
  "services" (include "osac.networking.enabledServices" . | fromJson)
  "excluded_ipv4_cidrs" ($networking.excludedIpv4Cidrs | default list)
  "provisioning_enabled" $networking.provisioningEnabled
-}}
{{- toJson $input -}}
{{- end -}}

{{/*
Service account names of the OSAC controllers that may write OSAC-managed
resources (ValidatingAdmissionPolicy). They mirror the subcharts' own
serviceAccountName helpers, which cannot be called with the umbrella's values.
*/}}
{{- define "osac.subchartFullname" -}}
{{- $values := index . 0 -}}
{{- $chartName := index . 1 -}}
{{- $release := index . 2 -}}
{{- if $values.fullnameOverride -}}
{{- $values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default $chartName $values.nameOverride -}}
{{- if contains $name $release -}}
{{- $release | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" $release $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "osac.operatorServiceAccountName" -}}
{{- $values := .Values.operator -}}
{{- (($values.serviceAccount | default dict).name) | default (include "osac.subchartFullname" (list $values "operator" .Release.Name)) -}}
{{- end -}}

{{- define "osac.bmfServiceAccountName" -}}
{{- $values := .Values.bmf -}}
{{- (($values.serviceAccount | default dict).name) | default (include "osac.subchartFullname" (list $values "bmf" .Release.Name)) -}}
{{- end -}}
