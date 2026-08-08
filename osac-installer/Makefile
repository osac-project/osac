INSTALLER_NAMESPACE ?= osac
VALUES_FILE ?= values/development/values.yaml
# Extra helm --set/--set-string args appended to install-osac's install command
# (e.g. e2e passing component image overrides), so the FIRST install already
# uses them instead of a separate patch applied after the fact.
EXTRA_HELM_ARGS ?=

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Installation

.PHONY: install
install: install-operators install-prereqs install-osac ## Full install (operators + prereqs + OSAC)

.PHONY: wait-for-api
wait-for-api: ## Wait for the Kubernetes API server to be consistently reachable
	@echo "Waiting for API server to be reachable..."
	@for i in $$(seq 1 30); do \
		if oc get --raw /version >/dev/null 2>&1; then \
			echo "API server is reachable."; \
			exit 0; \
		fi; \
		if [ "$$i" -eq 30 ]; then \
			echo "ERROR: API server still unreachable after 5 minutes." >&2; \
			exit 1; \
		fi; \
		echo "  Not yet reachable (attempt $$i/30), retrying in 10s..."; \
		sleep 10; \
	done

.PHONY: wait-for-operators-namespaces
wait-for-operators-namespaces: ## Wait for operator namespaces left Terminating by a prior uninstall
	@bash -c 'source scripts/lib.sh && \
		for ns in ansible-aap cert-manager-operator cert-manager openshift-storage metallb-system multicluster-engine openshift-cnv; do \
			wait_for_namespace_cleanup "$$ns"; \
		done'

.PHONY: install-operators
install-operators: wait-for-api wait-for-operators-namespaces ## Phase 1: Install OLM operators (cert-manager, AAP, LVMS, etc.)
	$(eval OCP_VERSION := $(shell oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2))
	helm upgrade --install osac-operators charts/osac-operators/ \
		--namespace osac-operators --create-namespace \
		--values $(VALUES_FILE) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--timeout 30m --wait

.PHONY: wait-for-prereqs-namespaces
wait-for-prereqs-namespaces: ## Wait for the keycloak namespace left Terminating by a prior uninstall
	@bash -c 'source scripts/lib.sh && wait_for_namespace_cleanup keycloak'

.PHONY: install-prereqs
install-prereqs: wait-for-prereqs-namespaces ## Phase 2: Configure prerequisites (certificates, keycloak, operator CRs)
	$(eval OCP_VERSION := $(shell oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2))
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'))
	helm upgrade --install osac-prereqs charts/osac-prereqs/ \
		--namespace osac-prereqs --create-namespace \
		--values $(VALUES_FILE) \
		--set osacNamespace=$(INSTALLER_NAMESPACE) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		$(if $(DOMAIN),--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN)) \
		$(if $(DOMAIN),--set keycloak.route.hostname=keycloak-keycloak.$(DOMAIN)) \
		--timeout 30m --wait-for-jobs

AAP_LICENSE_FILE ?= $(dir $(VALUES_FILE))license.zip

.PHONY: install-secrets
install-secrets: ## Create pre-install secrets (AAP license)
	@[[ -f "$(AAP_LICENSE_FILE)" ]] || { echo "ERROR: AAP license not found at $(AAP_LICENSE_FILE)"; exit 1; }
	@bash -c 'source scripts/lib.sh && wait_for_namespace_cleanup "$(INSTALLER_NAMESPACE)"'
	oc create namespace $(INSTALLER_NAMESPACE) --dry-run=client -o yaml | oc apply -f -
	# Server-side apply avoids last-applied-configuration (256KiB limit on large
	# license.zip) while remaining idempotent when the license file changes.
	oc create secret generic config-as-code-manifest-ig \
		--from-file=license.zip="$(AAP_LICENSE_FILE)" \
		-n $(INSTALLER_NAMESPACE) --dry-run=client -o yaml | oc apply --server-side -f -
	oc label secret config-as-code-manifest-ig \
		osac.openshift.io/project=osac-aap \
		-n $(INSTALLER_NAMESPACE) --overwrite

.PHONY: check-postgres
check-postgres: ## Verify in-cluster PostgreSQL is reachable before installing OSAC
	@bash -c 'source scripts/lib.sh && check_postgres_prerequisites "$(INSTALLER_NAMESPACE)" "$(VALUES_FILE)"'

.PHONY: install-osac
install-osac: helm-deps install-secrets check-postgres ## Phase 3: Install OSAC
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'))
	@[[ -n "$(DOMAIN)" ]] || { echo "ERROR: Could not determine cluster domain. Is oc logged in?"; exit 1; }
	helm upgrade --install osac charts/osac/ \
		--namespace $(INSTALLER_NAMESPACE) --create-namespace \
		--values $(VALUES_FILE) \
		--set service.externalHostname=fulfillment-api-$(INSTALLER_NAMESPACE).$(DOMAIN) \
		--set service.internalHostname=fulfillment-internal-api-$(INSTALLER_NAMESPACE).$(DOMAIN) \
		--set service.auth.issuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac \
		$(EXTRA_HELM_ARGS) \
		--timeout 40m --wait

.PHONY: uninstall
uninstall: ## Full uninstall (OSAC + prereqs + operators)
	-helm uninstall osac --namespace $(INSTALLER_NAMESPACE) --wait --timeout 20m
	-helm uninstall osac-prereqs --namespace osac-prereqs --wait --timeout 10m
	-helm uninstall osac-operators --namespace osac-operators

##@ Multi-Instance Deployment

BASE_NAMESPACE ?= osac-base

.PHONY: install-multi
install-multi: ## Full multi-instance setup (operators + prereqs + base)
	$(MAKE) install-operators
	$(MAKE) install-prereqs INSTALLER_NAMESPACE=$(BASE_NAMESPACE)
	$(MAKE) install-base

.PHONY: install-base
install-base: helm-deps-base install-secrets-base ## Phase 3: Install shared base layer (CRDs + AAP + PostgreSQL)
	helm upgrade --install osac-base charts/osac-base/ \
		--namespace $(BASE_NAMESPACE) --create-namespace \
		--values $(VALUES_FILE) \
		$(EXTRA_HELM_ARGS) \
		--timeout 40m --wait

.PHONY: install-secrets-base
install-secrets-base:
	@[[ -f "$(AAP_LICENSE_FILE)" ]] || { echo "ERROR: AAP license not found at $(AAP_LICENSE_FILE)"; exit 1; }
	@bash -c 'source scripts/lib.sh && wait_for_namespace_cleanup "$(BASE_NAMESPACE)"'
	oc create namespace $(BASE_NAMESPACE) --dry-run=client -o yaml | oc apply -f -
	oc label namespace $(BASE_NAMESPACE) osac.openshift.io/managed=true --overwrite
	oc create secret generic config-as-code-manifest-ig \
		--from-file=license.zip="$(AAP_LICENSE_FILE)" \
		-n $(BASE_NAMESPACE) --dry-run=client -o yaml | oc apply --server-side -f -
	oc label secret config-as-code-manifest-ig \
		osac.openshift.io/project=osac-aap \
		-n $(BASE_NAMESPACE) --overwrite

.PHONY: install-workload
install-workload: helm-deps-workload copy-base-secrets ## Phase 4: Install workload in NAMESPACE (repeatable)
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'))
	@[[ -n "$(NAMESPACE)" ]] || { echo "ERROR: NAMESPACE is required. Usage: make install-workload NAMESPACE=pool-abc123"; exit 1; }
	@[[ -n "$(DOMAIN)" ]] || { echo "ERROR: Could not determine cluster domain. Is oc logged in?"; exit 1; }
	helm upgrade --install osac-$(NAMESPACE) charts/osac-workload/ \
		--namespace $(NAMESPACE) --create-namespace \
		--values $(VALUES_FILE) \
		--set baseNamespace=$(BASE_NAMESPACE) \
		--set service.externalHostname=fulfillment-api-$(NAMESPACE).$(DOMAIN) \
		--set service.internalHostname=fulfillment-internal-api-$(NAMESPACE).$(DOMAIN) \
		--set service.auth.issuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac \
		--set operator.consoleProxy.disabled=true \
		--set operator.aap.url=http://osac-aap.$(BASE_NAMESPACE).svc.cluster.local/api/controller \
		$(EXTRA_HELM_ARGS) \
		--timeout 20m --wait

.PHONY: uninstall-workload
uninstall-workload: ## Uninstall workload from NAMESPACE
	@[[ -n "$(NAMESPACE)" ]] || { echo "ERROR: NAMESPACE is required."; exit 1; }
	-helm uninstall osac-$(NAMESPACE) --namespace $(NAMESPACE) --wait --timeout 10m

.PHONY: uninstall-base
uninstall-base: ## Uninstall shared base layer
	-helm uninstall osac-base --namespace $(BASE_NAMESPACE) --wait --timeout 20m

.PHONY: copy-base-secrets
copy-base-secrets:
	@[[ -n "$(NAMESPACE)" ]] || { echo "ERROR: NAMESPACE is required."; exit 1; }
	@oc create namespace $(NAMESPACE) --dry-run=client -o yaml | oc apply -f -
	@oc label namespace $(NAMESPACE) osac.openshift.io/managed=true --overwrite
	@for secret in osac-aap-api-token fulfillment-controller-credentials; do \
		echo "Copying secret $$secret from $(BASE_NAMESPACE) to $(NAMESPACE)..."; \
		oc get secret $$secret -n $(BASE_NAMESPACE) -o json \
			| jq 'del(.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.ownerReferences,.metadata.labels,.metadata.annotations) | .metadata.namespace = "$(NAMESPACE)"' \
			| oc apply -f -; \
	done

##@ Helm Chart Management

.PHONY: sync-charts
sync-charts: ## Update submodules and rebuild chart dependencies
	git submodule update --init --recursive
	helm dependency build charts/osac/

.PHONY: helm-deps
helm-deps: ## Build Helm chart dependencies
	helm dependency build charts/osac/

.PHONY: helm-deps-base
helm-deps-base: ## Build base chart dependencies
	helm dependency build charts/osac-base/

.PHONY: helm-deps-workload
helm-deps-workload: ## Build workload chart dependencies
	helm dependency build charts/osac-workload/

.PHONY: helm-lint
helm-lint: ## Lint all charts
	helm lint charts/osac-operators/
	helm lint charts/osac-prereqs/
	helm dependency build charts/osac/
	helm lint charts/osac/ --set service.externalHostname=lint --set service.internalHostname=lint
	helm dependency build charts/osac-base/
	helm lint charts/osac-base/ -f charts/osac-base/ci/default-values.yaml
	helm dependency build charts/osac-workload/
	helm lint charts/osac-workload/ -f charts/osac-workload/ci/default-values.yaml

.PHONY: helm-template
helm-template: ## Dry-run render all templates
	helm template osac-operators charts/osac-operators/ --values $(VALUES_FILE)
	helm template osac-prereqs charts/osac-prereqs/ --values $(VALUES_FILE)
	helm dependency build charts/osac/
	helm template osac charts/osac/ --values $(VALUES_FILE)

##@ Validation

.PHONY: helm-validate
helm-validate: helm-lint ## Validate all charts (lint + template)
	@for f in values/*/values.yaml; do \
		echo "Validating $$f..."; \
		helm template osac-operators charts/osac-operators/ --values "$$f" > /dev/null; \
		helm template osac-prereqs charts/osac-prereqs/ --values "$$f" > /dev/null; \
	done
	@echo "Validation passed."
