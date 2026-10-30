{{/*
Install preflight engine.

Runs at render time (helm install, helm upgrade, --dry-run=server) and checks
what can be known about the cluster before Helm creates anything. The checks
are listed in files/preflight/catalog.yaml; this file holds the logic for each
`kind` and the aggregation.

Evaluation order for one check (the first rule that applies decides):

  1. validation.enabled=false          -> skipped "validation disabled"
  2. requiredFor / when / unless gate  -> skipped "<service> not enabled"
  3. preflight.skip.<id>               -> skipped, with the user's reason
  4. no cluster access (offline)       -> unknown; never blocks
  5. platforms: [openshift] mismatch   -> skipped "not OpenShift"
  6. dependsOn did not pass            -> unknown "depends on <id>"
  7. kind handler                      -> pass | fail | warn | unknown | skipped

Outcome table (see `osac.preflight.run`):

  required + fail                      blocks
  required + unknown, cluster reachable, not caused by another check
                                       blocks (fail closed)
  warning + fail                       reported as `warn`, blocks only in
                                       preflight.mode=strict
  preflight.mode=report                never blocks
  offline render                       nothing blocks

A lookup of a kind the API server does not serve aborts the whole render, so
every handler asks osac.preflight.served (capabilities, or an Established CRD) before it calls `lookup`.

Reports carry names and keys of Secrets only, never their data.
*/}}

{{/* Value at a dotted path under .Values, as JSON {"v": <value>}. */}}
{{- define "osac.preflight.at" -}}
{{- $cur := .root.Values -}}
{{- range $seg := splitList "." .path -}}
  {{- if kindIs "map" $cur -}}
    {{- $cur = index $cur $seg -}}
  {{- else -}}
    {{- $cur = (index (dict) "none") -}}
  {{- end -}}
{{- end -}}
{{- dict "v" $cur | toJson -}}
{{- end -}}

{{/* "true" when the value at the path is set (non-empty / true), else "". */}}
{{- define "osac.preflight.truthy" -}}
{{- $v := (include "osac.preflight.at" .) | fromJson -}}
{{- if $v.v }}true{{ end -}}
{{- end -}}

{{/*
"true" when the API is served: Helm's capabilities say so, or the CRD that
defines the kind is Established. `helm template --dry-run=server` enables
lookup but leaves .Capabilities at their defaults, so capabilities alone would
report every operator-provided kind as missing there. The CRD name is guessed
as <lower-case kind>[s].<group>, which holds for every kind the catalog uses; a
wrong guess only falls back to the capabilities answer.
*/}}
{{- define "osac.preflight.served" -}}
{{- $gv := splitList "/" .apiVersion -}}
{{- if .root.Capabilities.APIVersions.Has (printf "%s/%s" .apiVersion .kind) }}true
{{- else if eq (len $gv) 2 -}}
  {{- $plural := lower .kind -}}
  {{- if not (hasSuffix "s" $plural) }}{{ $plural = printf "%ss" $plural }}{{ end -}}
  {{- $crd := lookup "apiextensions.k8s.io/v1" "CustomResourceDefinition" "" (printf "%s.%s" $plural (first $gv)) -}}
  {{- range (dig "status" "conditions" (list) $crd) -}}
    {{- if and (eq .type "Established") (eq .status "True") }}true{{ end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/* A handler result as JSON. */}}
{{- define "osac.preflight.result" -}}
{{- dict "state" .state "message" (.message | default "") "observed" (.observed | default (dict)) | toJson -}}
{{- end -}}

{{/*
Facts about the run that every check shares, as JSON: validation switch,
mode, skips, reachability, platform and whether a deployed revision exists.
*/}}
{{- define "osac.preflight.context" -}}
{{- $root := . -}}
{{- $pf := .Values.preflight | default dict -}}
{{- $ns := .Release.Namespace -}}
{{- $enabled := true -}}
{{- if hasKey (.Values.validation | default dict) "enabled" -}}
  {{- $enabled = .Values.validation.enabled -}}
{{- end -}}
{{/*
With validation disabled nothing may touch the cluster: a deployer without read
access (a namespace-scoped GitOps account, for one) must still be able to render
and install. Every lookup below therefore sits behind $enabled, and nested ifs
keep it that way whatever the template engine does with `and` operands.
*/}}
{{- $online := false -}}
{{- if $enabled -}}
  {{- if lookup "v1" "Namespace" "" "kube-system" -}}
    {{- $online = true -}}
  {{- end -}}
{{- end -}}
{{/*
"Upgrade" means a deployed revision of this release exists, not
.Release.IsUpgrade: a retry after a failed first install is still an install.
Release Secrets are read by name, newest first, instead of listing the namespace.
*/}}
{{- $deployed := false -}}
{{- $rev := int .Release.Revision -}}
{{- if and $online (gt $rev 1) -}}
  {{- range $k := untilStep 1 (int (add1 (min (sub $rev 1) 20))) 1 -}}
    {{- if not $deployed -}}
      {{- $s := lookup "v1" "Secret" $ns (printf "sh.helm.release.v1.%s.v%d" $root.Release.Name (sub $rev $k)) -}}
      {{- if and $s (eq (dig "metadata" "labels" "status" "" $s) "deployed") -}}
        {{- $deployed = true -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- $features := dict "core" true -}}
{{- range $svc := list "vmaas" "caas" "bmaas" "maas" -}}
  {{- $_ := set $features $svc (ne (include "osac.preflight.truthy" (dict "root" $root "path" (printf "global.services.%s.enabled" $svc))) "") -}}
{{- end -}}
{{/* The metering service is always deployed. */}}
{{- $_ := set $features "metering" true -}}
{{/*
Secrets the release creates itself (directly or through its Certificates). The
database ones are named off osac.pgPrefix, a fixed literal, and not off
osac.fullname, which changes with the release and chart names; the db-init
certificate follows the fullname.
*/}}
{{- $pg := include "osac.pgPrefix" $root -}}
{{- $owned := list (printf "%s-db-config" $pg) (printf "%s-db-client-cert" $pg) (printf "%s-db-metering-config" $pg) (printf "%s-db-metering-client-cert" $pg) (printf "%s-db-init-cert" (include "osac.fullname" $root)) -}}
{{- if include "osac.provisionFulfillmentKafkaUser" $root -}}
  {{- $owned = append $owned (include "osac.fulfillmentKafkaSecretName" $root) -}}
{{- end -}}
{{- $openshift := false -}}
{{- if $online -}}
  {{- if or ($root.Capabilities.APIVersions.Has "security.openshift.io/v1") (ne (include "osac.preflight.served" (dict "root" $root "apiVersion" "security.openshift.io/v1" "kind" "SecurityContextConstraints")) "") -}}
    {{- $openshift = true -}}
  {{- end -}}
{{- end -}}
{{- dict
  "enabled" $enabled
  "mode" ($pf.mode | default "enforce")
  "skip" ($pf.skip | default (dict))
  "online" $online
  "deployed" $deployed
  "openshift" $openshift
  "features" $features
  "owned" $owned
  | toJson -}}
{{- end -}}

{{/* Evaluate one catalog entry; args: root, ctx, check, byId. Returns the result as JSON. */}}
{{- define "osac.preflight.evaluate" -}}
{{- $root := .root -}}
{{- $ctx := .ctx -}}
{{- $c := .check -}}
{{- $sev := $c.severity -}}
{{- if and $ctx.deployed $c.severityUpgrade -}}
  {{- $sev = $c.severityUpgrade -}}
{{- end -}}
{{- if and $c.requiredWhen (not (include "osac.preflight.truthy" (dict "root" $root "path" $c.requiredWhen))) -}}
  {{- $sev = "warning" -}}
{{- end -}}
{{- if $c.requiredWhenAny -}}
  {{- $needed := false -}}
  {{- range $path := $c.requiredWhenAny -}}
    {{- if include "osac.preflight.truthy" (dict "root" $root "path" $path) -}}{{- $needed = true -}}{{- end -}}
  {{- end -}}
  {{- if not $needed -}}
    {{- $sev = "warning" -}}
  {{- end -}}
{{- end -}}
{{- $res := dict "id" $c.id "title" $c.title "category" $c.category "severity" $sev "requiredFor" $c.requiredFor "status" "pass" "message" "" "remediation" $c.remediation "docsURL" ($c.docsURL | default "") "observed" (dict) "skipReason" "" -}}
{{- $state := "" -}}
{{- $msg := "" -}}
{{- if not $ctx.enabled -}}
  {{- $state = "skipped" -}}
  {{- $msg = "validation disabled" -}}
{{- end -}}
{{- if not $state -}}
  {{- $on := false -}}
  {{- range $f := $c.requiredFor -}}
    {{- if index $ctx.features $f -}}{{- $on = true -}}{{- end -}}
  {{- end -}}
  {{- $anyOn := false -}}
  {{- range $path := ($c.whenAny | default list) -}}
    {{- if include "osac.preflight.truthy" (dict "root" $root "path" $path) -}}{{- $anyOn = true -}}{{- end -}}
  {{- end -}}
  {{- if not $on -}}
    {{- $state = "skipped" -}}
    {{- $msg = printf "%s not enabled" (first $c.requiredFor) -}}
  {{- else if and $c.when (not (include "osac.preflight.truthy" (dict "root" $root "path" $c.when))) -}}
    {{- $state = "skipped" -}}
    {{- $msg = printf "%s is not set" $c.when -}}
  {{- else if and $c.whenAny (not $anyOn) -}}
    {{- $state = "skipped" -}}
    {{- $msg = printf "none of %s is set" (join ", " $c.whenAny) -}}
  {{- else if and $c.unless (include "osac.preflight.truthy" (dict "root" $root "path" $c.unless)) -}}
    {{- $state = "skipped" -}}
    {{- $msg = printf "%s is set, so this release creates the resource itself" $c.unless -}}
  {{- end -}}
{{- end -}}
{{- if not $state -}}
  {{- if hasKey $ctx.skip $c.id -}}
    {{- $state = "skipped" -}}
    {{- $msg = (index $ctx.skip $c.id) | toString | default "skipped by preflight.skip" -}}
  {{- end -}}
{{- end -}}
{{- if not $state -}}
  {{- if not $ctx.online -}}
    {{- $state = "unknown" -}}
    {{- $msg = "not evaluated: no cluster access (offline render)" -}}
  {{- else if and (has "openshift" ($c.platforms | default list)) (not $ctx.openshift) -}}
    {{- $state = "skipped" -}}
    {{- $msg = "not OpenShift" -}}
  {{- end -}}
{{- end -}}
{{- $blockedBy := list -}}
{{- if not $state -}}
  {{- range $dep := ($c.dependsOn | default list) -}}
    {{- $d := index $.byId $dep -}}
    {{- if and $d (has $d.status (list "fail" "warn" "unknown")) -}}
      {{- $blockedBy = append $blockedBy $dep -}}
    {{- end -}}
  {{- end -}}
  {{- if $blockedBy -}}
    {{- $state = "unknown" -}}
    {{- $msg = printf "not evaluated: depends on %s" (join ", " $blockedBy) -}}
  {{- end -}}
{{- end -}}
{{- if not $state -}}
  {{- $h := include (printf "osac.preflight.kind.%s" $c.kind) (dict "root" $root "ctx" $ctx "check" $c "params" ($c.params | default (dict))) | fromJson -}}
  {{- $_ := set $res "observed" $h.observed -}}
  {{- $msg = $h.message -}}
  {{- if eq $h.state "ok" -}}
    {{- $state = "pass" -}}
  {{- else if eq $h.state "bad" -}}
    {{- $state = ternary "fail" "warn" (eq $sev "required") -}}
  {{- else if eq $h.state "soft" -}}
    {{- $state = "warn" -}}
  {{- else -}}
    {{- $state = $h.state -}}
  {{- end -}}
{{- end -}}
{{- $_ := set $res "status" $state -}}
{{- $_ := set $res "message" $msg -}}
{{- if eq $state "skipped" -}}
  {{- $_ := set $res "skipReason" $msg -}}
{{- end -}}
{{- if $blockedBy -}}
  {{- $_ := set $res "dependsOn" $blockedBy -}}
{{- end -}}
{{- $res | toJson -}}
{{- end -}}

{{/*
Run every check and return the PreflightReport as JSON. The report validates
against files/preflight/report.schema.json.
*/}}
{{- define "osac.preflight.run" -}}
{{- $root := . -}}
{{- $ctx := include "osac.preflight.context" . | fromJson -}}
{{- $catalog := .Files.Get "files/preflight/catalog.yaml" | fromYamlArray -}}
{{- $results := list -}}
{{- $byId := dict -}}
{{- $known := dict -}}
{{- range $c := $catalog -}}
  {{- $_ := set $known $c.id true -}}
{{- end -}}
{{- range $c := $catalog -}}
  {{- $r := include "osac.preflight.evaluate" (dict "root" $root "ctx" $ctx "check" $c "byId" $byId) | fromJson -}}
  {{- $_ := set $byId $c.id $r -}}
  {{- $results = append $results $r -}}
{{- end -}}
{{- $unknownSkips := list -}}
{{- range $id, $_ := $ctx.skip -}}
  {{- if not (hasKey $known $id) -}}
    {{- $unknownSkips = append $unknownSkips $id -}}
  {{- end -}}
{{- end -}}
{{- if $unknownSkips -}}
  {{- $results = append $results (dict "id" "preflight-skip-unknown-id" "title" "preflight.skip names only known check ids" "category" "cluster" "severity" "warning" "requiredFor" (list "core") "status" "warn" "message" (printf "preflight.skip names unknown check id(s): %s" (join ", " (sortAlpha $unknownSkips))) "remediation" "Fix the spelling; the ids are listed in files/preflight/catalog.yaml." "docsURL" "" "observed" (dict "unknownIds" (sortAlpha $unknownSkips)) "skipReason" "") -}}
{{- end -}}
{{- $summary := dict "pass" 0 "fail" 0 "warn" 0 "unknown" 0 "skipped" 0 "blocking" 0 -}}
{{- $out := list -}}
{{- range $r := $results -}}
  {{- $blocking := false -}}
  {{- if and (eq $r.status "fail") (eq $r.severity "required") -}}
    {{- $blocking = true -}}
  {{- else if and (eq $r.status "unknown") $ctx.online (eq $r.severity "required") (not (hasKey $r "dependsOn")) -}}
    {{- $blocking = true -}}
  {{- else if and (eq $ctx.mode "strict") (eq $r.status "warn") -}}
    {{- $blocking = true -}}
  {{- end -}}
  {{- $_ := set $r "blocking" $blocking -}}
  {{- $_ := set $summary $r.status (add1 (index $summary $r.status)) -}}
  {{- if $blocking -}}
    {{- $_ := set $summary "blocking" (add1 (index $summary "blocking")) -}}
  {{- end -}}
  {{- $out = append $out $r -}}
{{- end -}}
{{- dict
  "apiVersion" "preflight.osac.openshift.io/v1alpha1"
  "kind" "PreflightReport"
  "phase" "pre-install"
  "tier" "render"
  "mode" $ctx.mode
  "generatedAt" (dateInZone "2006-01-02T15:04:05Z07:00" now "UTC")
  "helmVersion" .Capabilities.HelmVersion.Version
  "kubeVersion" ($ctx.online | ternary .Capabilities.KubeVersion.Version (index (dict) "none"))
  "chart" (dict "name" .Chart.Name "version" .Chart.Version)
  "release" (dict "name" .Release.Name "namespace" .Release.Namespace "revision" (int .Release.Revision) "action" ($ctx.deployed | ternary "upgrade" "install"))
  "summary" $summary
  "results" $out
  | toJson -}}
{{- end -}}

{{/*
The text of the single failure: every blocking problem with its fix, a hint
to skip, and the report as one JSON line at column 0 for scripts to grep.
*/}}
{{- define "osac.preflight.failMessage" -}}
{{- $r := . -}}
{{- $warns := 0 -}}
{{- range $r.results }}{{ if and (eq .status "warn") (not .blocking) }}{{ $warns = add1 $warns }}{{ end }}{{ end -}}
OSAC preflight: {{ $r.summary.blocking }} blocking problem(s), {{ $warns }} warning(s). Nothing was changed.
{{ range $r.results }}
{{- if .blocking }}
  {{ upper .status | printf "%-7s" }} {{ .id }}: {{ .message }}
          Fix: {{ .remediation }}
{{ end }}
{{- end }}
{{- range $r.results }}
{{- if and (eq .status "warn") (not .blocking) }}
  WARN    {{ .id }}: {{ .message }}
{{ end }}
{{- end }}
To skip a check: --set preflight.skip.<id>="<reason>"
To only report, never block: --set preflight.mode=report
{{- /*
The JSON line is embedded in an error message. Tools that scan error text for
file:line:column (helm-unittest does) would take the clock time for a source
location, so the colons of the timestamp are written as the JSON escape \u003a.
Every JSON parser decodes it back to the same string.
*/}}
OSAC-PREFLIGHT-JSON v1: {{ $r | toJson | replace $r.generatedAt (replace ":" "\\u003a" $r.generatedAt) }}
{{ end -}}

{{/* ===================== kind handlers ===================== */}}

{{/* crd-exists: every CRD in `crds` exists, or at least one of `anyCrds` does. */}}
{{- define "osac.preflight.kind.crd-exists" -}}
{{- $p := .params -}}
{{- $found := list -}}
{{- $missing := list -}}
{{- range $n := (concat ($p.crds | default list) ($p.anyCrds | default list)) -}}
  {{- if lookup "apiextensions.k8s.io/v1" "CustomResourceDefinition" "" $n -}}
    {{- $found = append $found $n -}}
  {{- else -}}
    {{- $missing = append $missing $n -}}
  {{- end -}}
{{- end -}}
{{- $ok := false -}}
{{- if $p.anyCrds -}}
  {{- $ok = gt (len $found) 0 -}}
{{- else -}}
  {{- $ok = eq (len $missing) 0 -}}
{{- end -}}
{{- if $ok -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "found %s" (join ", " $found)) "observed" (dict "found" $found "missing" $missing)) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "CustomResourceDefinition %s not found" (join " or " $missing)) "observed" (dict "found" $found "missing" $missing)) -}}
{{- end -}}
{{- end -}}

{{/* version-range: ClusterVersion desired version at least min, and at most max when max is set (patch level). */}}
{{- define "osac.preflight.kind.version-range" -}}
{{- $p := .params -}}
{{- $cv := dict -}}
{{- if include "osac.preflight.served" (dict "root" .root "apiVersion" "config.openshift.io/v1" "kind" "ClusterVersion") -}}
  {{- $cv = lookup "config.openshift.io/v1" "ClusterVersion" "" "version" -}}
{{- end -}}
{{- $v := regexReplaceAll "-.*$" (dig "status" "desired" "version" "" $cv) "" -}}
{{- $max := $p.max | default "" -}}
{{- $constraint := printf ">=%s" $p.min -}}
{{- $wanted := printf "%s or later" $p.min -}}
{{- if $max -}}
  {{- $constraint = printf ">=%s <=%s" $p.min $max -}}
  {{- $wanted = printf "inside the validated range %s to %s" $p.min $max -}}
{{- end -}}
{{- $observed := dict "version" $v "min" $p.min -}}
{{- if $max -}}{{- $_ := set $observed "max" $max -}}{{- end -}}
{{- if not $v -}}
{{- include "osac.preflight.result" (dict "state" "unknown" "message" "could not read the ClusterVersion") -}}
{{- else if semverCompare $constraint $v -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "OpenShift %s is %s" $v $wanted) "observed" $observed) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "OpenShift %s is not %s" $v $wanted) "observed" $observed) -}}
{{- end -}}
{{- end -}}

{{/* namespace-active: the release namespace exists and is not terminating. */}}
{{- define "osac.preflight.kind.namespace-active" -}}
{{- $ns := .root.Release.Namespace -}}
{{- $o := lookup "v1" "Namespace" "" $ns -}}
{{- if not $o -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "Namespace %s does not exist" $ns) "observed" (dict "namespace" $ns "found" false)) -}}
{{- else if eq (dig "status" "phase" "" $o) "Terminating" -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "Namespace %s is Terminating" $ns) "observed" (dict "namespace" $ns "phase" "Terminating")) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "Namespace %s is active" $ns) "observed" (dict "namespace" $ns "phase" "Active")) -}}
{{- end -}}
{{- end -}}

{{/*
nodes-schedulable: at least one node is Ready, not cordoned, and free of the
taints that keep ordinary pods off (node.kubernetes.io/*, control-plane).
Custom taints are ignored: pod tolerations are not known here.
*/}}
{{- define "osac.preflight.kind.nodes-schedulable" -}}
{{- $nodes := (lookup "v1" "Node" "" "").items | default list -}}
{{- $ok := 0 -}}
{{- $notReady := 0 -}}
{{- $cordoned := 0 -}}
{{- $tainted := 0 -}}
{{- range $n := $nodes -}}
  {{- $ready := false -}}
  {{- range (dig "status" "conditions" (list) $n) -}}
    {{- if and (eq .type "Ready") (eq .status "True") -}}{{- $ready = true -}}{{- end -}}
  {{- end -}}
  {{- $blockingTaint := false -}}
  {{- range (dig "spec" "taints" (list) $n) -}}
    {{- if and (has .effect (list "NoSchedule" "NoExecute")) (or (hasPrefix "node.kubernetes.io/" .key) (has .key (list "node-role.kubernetes.io/master" "node-role.kubernetes.io/control-plane"))) -}}
      {{- $blockingTaint = true -}}
    {{- end -}}
  {{- end -}}
  {{- if not $ready -}}
    {{- $notReady = add1 $notReady -}}
  {{- else if dig "spec" "unschedulable" false $n -}}
    {{- $cordoned = add1 $cordoned -}}
  {{- else if $blockingTaint -}}
    {{- $tainted = add1 $tainted -}}
  {{- else -}}
    {{- $ok = add1 $ok -}}
  {{- end -}}
{{- end -}}
{{- $obs := dict "nodes" (len $nodes) "schedulable" $ok "notReady" $notReady "cordoned" $cordoned "tainted" $tainted -}}
{{- if gt $ok 0 -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "%d of %d node(s) can run new pods" $ok (len $nodes)) "observed" $obs) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "no node can run new pods (%d node(s): %d not Ready, %d cordoned, %d tainted)" (len $nodes) $notReady $cordoned $tainted) "observed" $obs) -}}
{{- end -}}
{{- end -}}

{{/* storageclass-usable: a StorageClass is marked as the default. */}}
{{- define "osac.preflight.kind.storageclass-usable" -}}
{{- $classes := (lookup "storage.k8s.io/v1" "StorageClass" "" "").items | default list -}}
{{- $defaults := list -}}
{{- range $sc := $classes -}}
  {{- $ann := dig "metadata" "annotations" (dict) $sc -}}
  {{- if or (eq (index $ann "storageclass.kubernetes.io/is-default-class" | default "") "true") (eq (index $ann "storageclass.beta.kubernetes.io/is-default-class" | default "") "true") -}}
    {{- $defaults = append $defaults $sc.metadata.name -}}
  {{- end -}}
{{- end -}}
{{- $obs := dict "storageClasses" (len $classes) "defaults" $defaults -}}
{{- if $defaults -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "default StorageClass: %s" (join ", " $defaults)) "observed" $obs) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "no default StorageClass is set (%d StorageClass(es) exist)" (len $classes)) "observed" $obs) -}}
{{- end -}}
{{- end -}}

{{/*
condition-true: a named object has a condition with status True.
params: apiVersion; kind or kindFrom; name or nameFrom; namespace or
namespaceFrom (default: release namespace); clusterScopedKinds (default
namespaced) or scope; condition.
*/}}
{{- define "osac.preflight.kind.condition-true" -}}
{{- $root := .root -}}
{{- $p := .params -}}
{{- $kind := $p.kind | default "" -}}
{{- if $p.kindFrom -}}{{- $kind = (include "osac.preflight.at" (dict "root" $root "path" $p.kindFrom) | fromJson).v | default "" -}}{{- end -}}
{{- $name := $p.name | default "" -}}
{{- if $p.nameFrom -}}{{- $name = (include "osac.preflight.at" (dict "root" $root "path" $p.nameFrom) | fromJson).v | default "" -}}{{- end -}}
{{- $ns := $p.namespace | default $root.Release.Namespace -}}
{{- if $p.namespaceFrom -}}{{- $ns = (include "osac.preflight.at" (dict "root" $root "path" $p.namespaceFrom) | fromJson).v | default $root.Release.Namespace -}}{{- end -}}
{{- $cluster := or (eq ($p.scope | default "") "cluster") (has $kind ($p.clusterScopedKinds | default list)) -}}
{{- if or (not $kind) (not $name) -}}
{{- include "osac.preflight.result" (dict "state" "unknown" "message" "the kind or name to check is not configured") -}}
{{- else if not (include "osac.preflight.served" (dict "root" $root "apiVersion" $p.apiVersion "kind" $kind)) -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "%s %s is not served by this cluster" $p.apiVersion $kind) "observed" (dict "apiVersion" $p.apiVersion "kind" $kind "served" false)) -}}
{{- else -}}
  {{- $o := lookup $p.apiVersion $kind ($cluster | ternary "" $ns) $name -}}
  {{- $obs := dict "apiVersion" $p.apiVersion "kind" $kind "name" $name -}}
  {{- if not $cluster -}}{{- $_ := set $obs "namespace" $ns -}}{{- end -}}
  {{- $status := "" -}}
  {{- $reason := "" -}}
  {{- range (dig "status" "conditions" (list) $o) -}}
    {{- if eq .type $p.condition -}}
      {{- $status = .status -}}
      {{- $reason = (.reason | default "") -}}
    {{- end -}}
  {{- end -}}
  {{- if not $o -}}
  {{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "%s %s not found" $kind $name) "observed" (set $obs "found" false)) -}}
  {{- else if eq $status "True" -}}
  {{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "%s %s is %s" $kind $name $p.condition) "observed" (set $obs "found" true)) -}}
  {{- else -}}
  {{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "%s %s is not %s%s" $kind $name $p.condition (ternary (printf " (%s)" $reason) "" (ne $reason ""))) "observed" (set $obs "found" true)) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
resource-field-equals: the first object of a kind (listed, or by name) has a
field equal to a value. params: apiVersion, kind, scope, field, equals.
*/}}
{{- define "osac.preflight.kind.resource-field-equals" -}}
{{- $p := .params -}}
{{- if not (include "osac.preflight.served" (dict "root" .root "apiVersion" $p.apiVersion "kind" $p.kind)) -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "%s %s is not served by this cluster" $p.apiVersion $p.kind) "observed" (dict "served" false)) -}}
{{- else -}}
  {{- $items := (lookup $p.apiVersion $p.kind ($p.namespace | default "") "").items | default list -}}
  {{- if not $items -}}
  {{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "no %s resource found" $p.kind) "observed" (dict "kind" $p.kind "found" false)) -}}
  {{- else -}}
    {{- $o := first $items -}}
    {{- $got := $o -}}
    {{- range $seg := splitList "." $p.field -}}
      {{- if kindIs "map" $got -}}{{- $got = index $got $seg -}}{{- else -}}{{- $got = (index (dict) "none") -}}{{- end -}}
    {{- end -}}
    {{- $obs := dict "kind" $p.kind "name" $o.metadata.name "field" $p.field "value" (ternary "<unset>" (toString $got) (kindIs "invalid" $got)) -}}
    {{- if eq (toString $got) (toString $p.equals) -}}
    {{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "%s %s has %s=%s" $p.kind $o.metadata.name $p.field (toString $p.equals)) "observed" $obs) -}}
    {{- else -}}
    {{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "%s %s has %s=%s, expected %s" $p.kind $o.metadata.name $p.field (index $obs "value") (toString $p.equals)) "observed" $obs) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
secret-keys: Secrets referenced by the values exist with the listed keys.
params:
  connections  values paths of connection lists
               (- secret: {name, items: [{key, param}]})
  secrets      explicit entries: name|nameFrom, keys|keysFrom, labels
  urlParam     the `param` whose key holds a postgres URL to parse
Secrets this release creates itself (ctx.owned) are not required to exist.
Only names and keys reach the report; a URL is reduced to scheme, host, port
and database.
*/}}
{{- define "osac.preflight.kind.secret-keys" -}}
{{- $root := .root -}}
{{- $ctx := .ctx -}}
{{- $p := .params -}}
{{- $ns := $root.Release.Namespace -}}
{{- $reqs := list -}}
{{- range $path := ($p.connections | default list) -}}
  {{- range $src := ((include "osac.preflight.at" (dict "root" $root "path" $path) | fromJson).v | default list) -}}
    {{- $s := $src.secret | default (dict) -}}
    {{- if $s.name -}}
      {{- $keys := list -}}
      {{- $urlKey := "" -}}
      {{- range $it := ($s.items | default list) -}}
        {{- $keys = append $keys $it.key -}}
        {{- if and $p.urlParam (eq ($it.param | default "") $p.urlParam) -}}{{- $urlKey = $it.key -}}{{- end -}}
      {{- end -}}
      {{- $reqs = append $reqs (dict "name" $s.name "keys" $keys "labels" (dict) "urlKey" $urlKey) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- range $e := ($p.secrets | default list) -}}
  {{- $name := $e.name | default "" -}}
  {{- if $e.nameFrom -}}{{- $name = (include "osac.preflight.at" (dict "root" $root "path" $e.nameFrom) | fromJson).v | default "" -}}{{- end -}}
  {{- $keys := $e.keys | default list -}}
  {{- range $kp := ($e.keysFrom | default list) -}}
    {{- $keys = append $keys ((include "osac.preflight.at" (dict "root" $root "path" $kp) | fromJson).v | default "") -}}
  {{- end -}}
  {{- if $name -}}
    {{- $reqs = append $reqs (dict "name" $name "keys" (compact $keys) "labels" ($e.labels | default (dict)) "urlKey" "") -}}
  {{- end -}}
{{- end -}}
{{- $problems := list -}}
{{- $seen := list -}}
{{- $obsSecrets := list -}}
{{- range $r := $reqs -}}
  {{- if not (or (has $r.name $ctx.owned) (has $r.name $seen)) -}}
    {{- $seen = append $seen $r.name -}}
    {{- $o := lookup "v1" "Secret" $ns $r.name -}}
    {{- $missingKeys := list -}}
    {{- if not $o -}}
      {{- $problems = append $problems (printf "Secret %s not found" $r.name) -}}
      {{- $obsSecrets = append $obsSecrets (dict "name" $r.name "found" false) -}}
    {{- else -}}
      {{- $data := $o.data | default (dict) -}}
      {{- range $k := $r.keys -}}
        {{- if not (index $data $k) -}}{{- $missingKeys = append $missingKeys $k -}}{{- end -}}
      {{- end -}}
      {{- if $missingKeys -}}
        {{- $problems = append $problems (printf "Secret %s is missing key(s) %s" $r.name (join ", " $missingKeys)) -}}
      {{- end -}}
      {{- $lbl := dig "metadata" "labels" (dict) $o -}}
      {{- range $lk, $lv := $r.labels -}}
        {{- if ne (index $lbl $lk | default "") $lv -}}
          {{- $problems = append $problems (printf "Secret %s lacks label %s=%s" $r.name $lk $lv) -}}
        {{- end -}}
      {{- end -}}
      {{- $entry := dict "name" $r.name "found" true "missingKeys" $missingKeys -}}
      {{- if and $r.urlKey (index $data $r.urlKey) -}}
        {{- $url := (index $data $r.urlKey) | b64dec -}}
        {{- if regexMatch "^postgres(ql)?://([^@/]*@)?[^:/?#@]+(:[0-9]+)?(/[^?]*)?(\\?.*)?$" $url -}}
          {{- $parts := regexReplaceAll "^(postgres(?:ql)?)://(?:[^@/]*@)?([^:/?#@]+)(?::([0-9]+))?(?:/([^?]*))?(?:\\?.*)?$" $url "${1}|${2}|${3}|${4}" | splitList "|" -}}
          {{- $_ := set $entry "url" (dict "scheme" (index $parts 0) "host" (index $parts 1) "port" (index $parts 2) "database" (index $parts 3)) -}}
        {{- else -}}
          {{/* Not a shape the redaction can describe (a keyword DSN, say): record that, never the value. */}}
          {{- $_ := set $entry "url" (dict "parsed" false) -}}
        {{- end -}}
      {{- end -}}
      {{- $obsSecrets = append $obsSecrets $entry -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- $obs := dict "secrets" $obsSecrets -}}
{{- if $problems -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (join "; " $problems) "observed" $obs) -}}
{{- else if not $obsSecrets -}}
{{- include "osac.preflight.result" (dict "state" "skipped" "message" "no Secret is referenced that this release does not create itself" "observed" $obs) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "%d Secret(s) found with the required keys" (len $obsSecrets)) "observed" $obs) -}}
{{- end -}}
{{- end -}}

{{/* configmap-keys: ConfigMaps referenced by the values exist. params.configmaps: nameFrom|name, keys, optional. */}}
{{- define "osac.preflight.kind.configmap-keys" -}}
{{- $root := .root -}}
{{- $ns := $root.Release.Namespace -}}
{{- $problems := list -}}
{{- $obsList := list -}}
{{- range $e := (.params.configmaps | default list) -}}
  {{- $name := $e.name | default "" -}}
  {{- if $e.nameFrom -}}{{- $name = (include "osac.preflight.at" (dict "root" $root "path" $e.nameFrom) | fromJson).v | default "" -}}{{- end -}}
  {{- if $name -}}
    {{- $o := lookup "v1" "ConfigMap" $ns $name -}}
    {{- if not $o -}}
      {{- $problems = append $problems (printf "ConfigMap %s not found" $name) -}}
      {{- $obsList = append $obsList (dict "name" $name "found" false) -}}
    {{- else -}}
      {{- $missing := list -}}
      {{- range $k := ($e.keys | default list) -}}
        {{- if not (hasKey (dig "data" (dict) $o) $k) -}}{{- $missing = append $missing $k -}}{{- end -}}
      {{- end -}}
      {{- if $missing -}}{{- $problems = append $problems (printf "ConfigMap %s is missing key(s) %s" $name (join ", " $missing)) -}}{{- end -}}
      {{- $obsList = append $obsList (dict "name" $name "found" true "missingKeys" $missing) -}}
    {{- end -}}
  {{- else if not $e.optional -}}
    {{- $problems = append $problems "a required ConfigMap name is not configured" -}}
  {{- end -}}
{{- end -}}
{{- $obs := dict "configMaps" $obsList -}}
{{- if $problems -}}
{{- include "osac.preflight.result" (dict "state" "bad" "message" (join "; " $problems) "observed" $obs) -}}
{{- else if not $obsList -}}
{{- include "osac.preflight.result" (dict "state" "skipped" "message" "no ConfigMap is referenced" "observed" $obs) -}}
{{- else -}}
{{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "%d ConfigMap(s) found" (len $obsList)) "observed" $obs) -}}
{{- end -}}
{{- end -}}

{{/*
service-has-endpoints: the host in a value names an in-cluster Service
(<svc>, <svc>.<namespace>, or <svc>.<namespace>.svc[.<domain>]) whose
EndpointSlices have a ready address. A Service that exists but has no ready
address yet is only a warning: the db-init hook retries, and the database is
often still starting when the install begins. Any other host is outside the
cluster and skipped. A two-label name counts as a Service only when that namespace exists,
so an external name such as db.corp is not mistaken for one. When the value is
blank, params.hostHelper names the template that holds the default the
workload itself uses.
*/}}
{{- define "osac.preflight.kind.service-has-endpoints" -}}
{{- $root := .root -}}
{{- $host := (include "osac.preflight.at" (dict "root" $root "path" .params.hostFrom) | fromJson).v | default "" -}}
{{- if and (not $host) .params.hostHelper -}}
  {{- $host = include .params.hostHelper $root -}}
{{- end -}}
{{- $parts := splitList "." $host -}}
{{- $svc := "" -}}
{{- $ns := "" -}}
{{- if eq (len $parts) 1 -}}
  {{- $svc = first $parts -}}
  {{- $ns = $root.Release.Namespace -}}
{{- else if and (ge (len $parts) 3) (eq (index $parts 2) "svc") -}}
  {{- $svc = index $parts 0 -}}
  {{- $ns = index $parts 1 -}}
{{- else if eq (len $parts) 2 -}}
  {{- if lookup "v1" "Namespace" "" (index $parts 1) -}}
    {{- $svc = index $parts 0 -}}
    {{- $ns = index $parts 1 -}}
  {{- end -}}
{{- end -}}
{{- if not $host -}}
{{- include "osac.preflight.result" (dict "state" "unknown" "message" (printf "%s is not set" .params.hostFrom)) -}}
{{- else if not $svc -}}
{{- include "osac.preflight.result" (dict "state" "skipped" "message" (printf "%s is outside the cluster; reachability is not checked at render time" .params.hostFrom) "observed" (dict "host" $host)) -}}
{{- else -}}
  {{- $slices := (lookup "discovery.k8s.io/v1" "EndpointSlice" $ns "").items | default list -}}
  {{- $found := false -}}
  {{- $ready := 0 -}}
  {{- range $sl := $slices -}}
    {{- if eq (dig "metadata" "labels" "kubernetes.io/service-name" "" $sl) $svc -}}
      {{- $found = true -}}
      {{- range (dig "endpoints" (list) $sl) -}}
        {{- if and (ne (dig "conditions" "ready" true .) false) (.addresses | default list) -}}
          {{- $ready = add1 $ready -}}
        {{- end -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- $obs := dict "service" $svc "namespace" $ns "readyAddresses" $ready -}}
  {{- if not $found -}}
  {{- include "osac.preflight.result" (dict "state" "bad" "message" (printf "Service %s in namespace %s not found" $svc $ns) "observed" $obs) -}}
  {{- else if gt $ready 0 -}}
  {{- include "osac.preflight.result" (dict "state" "ok" "message" (printf "Service %s in namespace %s has %d ready address(es)" $svc $ns $ready) "observed" $obs) -}}
  {{- else -}}
  {{- include "osac.preflight.result" (dict "state" "soft" "message" (printf "Service %s in namespace %s has no ready endpoints yet; the db-init hook retries for up to 5 minutes" $svc $ns) "observed" $obs) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
kafka-access: with the in-cluster Kafka (kafka.enabled) the Kafka resource is
Ready; with an external cluster the Secrets in service.kafka.connection exist.
*/}}
{{- define "osac.preflight.kind.kafka-access" -}}
{{- if .root.Values.kafka.enabled -}}
{{- include "osac.preflight.kind.condition-true" (dict "root" .root "ctx" .ctx "check" .check "params" (dict "apiVersion" "kafka.strimzi.io/v1" "kind" "Kafka" "nameFrom" "kafka.clusterName" "namespaceFrom" "kafka.clusterNamespace" "condition" "Ready")) -}}
{{- else -}}
{{- include "osac.preflight.kind.secret-keys" (dict "root" .root "ctx" .ctx "check" .check "params" (dict "connections" (list "service.kafka.connection"))) -}}
{{- end -}}
{{- end -}}
