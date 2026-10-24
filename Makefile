IMAGE_NAME ?= osac-dev
CARBONITE_IMAGE_NAME ?= osac-dev-carbonite
DISTROBOX_NAME ?= osac
CARBONITE_STATE_DIR ?= $(if $(XDG_STATE_HOME),$(XDG_STATE_HOME),$(HOME)/.local/state)/osac-carbonite-kind
CARBONITE_NAME ?= osac-carbonite-kind
HOME_DIR ?= $(HOME)
CONTAINER_CMD ?= $(shell command -v podman 2>/dev/null || echo docker)
DISTROBOX_CONTEXT := tools/distrobox
CARBONITE_CONTEXT := tools/carbonite
OPENSHELL_CMD ?= openshell
CARBONITE_GATEWAY ?= openshell
CARBONITE_POLICY ?= $(CARBONITE_STATE_DIR)/policy-kind.yaml
CARBONITE_WORKDIR ?= /sandbox/osac
CARBONITE_VOLUME ?= $(CARBONITE_NAME)-workspace
CARBONITE_KUBECONFIG_VOLUME = $(CARBONITE_NAME)-kubeconfig
CARBONITE_WORKSPACE ?= osac-kind
CARBONITE_CREATE_ARGS ?=
CARBONITE_KUBECONFIG_DIR ?= $(CARBONITE_STATE_DIR)/kube
export CARBONITE_KUBECONFIG_DIR
# Local images need a tag; OpenShell 0.1 no longer resolves community aliases.
CARBONITE_IMAGE_REF := $(if $(findstring :,$(CARBONITE_IMAGE_NAME)),$(CARBONITE_IMAGE_NAME),$(CARBONITE_IMAGE_NAME):latest)
CARBONITE_RUN = $(OPENSHELL_CMD) --gateway "$(CARBONITE_GATEWAY)" --workspace "$(CARBONITE_WORKSPACE)"
ARGS ?=
USE_CARBONITE ?= $(OSAC_USE_CARBONITE)
ifneq ($(USE_CARBONITE),)
ifneq ($(filter true false,$(USE_CARBONITE)),$(USE_CARBONITE))
$(error USE_CARBONITE must be true or false)
endif
endif
DEV_CONTAINER_SUFFIX := $(if $(filter true,$(USE_CARBONITE)),carb,distrobox)

.PHONY: image enter claude stop rm rebuild status help \
	image-distrobox enter-distrobox claude-distrobox stop-distrobox rm-distrobox \
	rebuild-distrobox status-distrobox \
	check-carb image-carb enter-carb claude-carb stop-carb rm-carb rebuild-carb status-carb

define carbonite_require_commands
@set -eu; \
for cmd in $(1); do \
	if ! command -v "$$cmd" >/dev/null 2>&1; then \
		printf 'Required host command not found: %s\nInstall it or set CONTAINER_CMD / OPENSHELL_CMD to its executable path. See README.md (Carbonite / OpenShell).\n' "$$cmd" >&2; \
		exit 1; \
	fi; \
done
endef

define carbonite_check_engine
@if ! $(CONTAINER_CMD) info >/dev/null; then \
	printf 'Cannot use container engine %s. Check its host-side setup and permissions before retrying.\n' "$(CONTAINER_CMD)" >&2; \
	exit 1; \
fi
endef

# Confirm the selected gateway attached the credential volume before entry.
define carbonite_verify_kubeconfig
@set -eu; \
if [ -n "$${CARBONITE_KUBECONFIG_DIR:-}" ]; then \
	if ! $(CARBONITE_RUN) sandbox exec --name "$(CARBONITE_NAME)" --no-login-shell --no-tty -- \
		/bin/sh -c 'test "$$KUBECONFIG" = /sandbox/kube/config && test -r "$$KUBECONFIG" && test ! -w "$$KUBECONFIG"'; then \
		printf 'The mounted kubeconfig is not readable and read-only in the sandbox; check the local gateway, volume source, ownership, and SELinux label.\n' >&2; \
		exit 1; \
	fi; \
fi
endef

# Query failures must fail the target, rather than look like a missing sandbox.
define carbonite_state
page_token=; \
while :; do \
	sandboxes="$$( $(CARBONITE_RUN) sandbox list --output json --page-size 1000 --page-token "$$page_token" )"; \
	phase="$$(printf '%s\n' "$$sandboxes" | jq -er --arg name "$(CARBONITE_NAME)" \
		'.sandboxes | map(select(.name == $$name)) | if length == 0 then "Absent" else .[0].phase end')"; \
	[ "$$phase" = Absent ] || break; \
	page_token="$$(printf '%s\n' "$$sandboxes" | jq -r '.next_page_token // ""')"; \
	[ -n "$$page_token" ] || break; \
done;
endef

# The optional host-only manager reconciles the sandbox's short-lived SSH grant.
# Ordinary Carbonite entry remains unchanged until the separate enabler installs it.
define carbonite_prepare_host_build
@set -eu; \
manager="$$HOME/.local/libexec/osac-host-build-manager"; \
if [ -L "$$manager" ]; then \
	printf 'Carbonite host-build manager must not be a symlink: %s\n' "$$manager" >&2; \
	exit 1; \
fi; \
if [ -e "$$manager" ]; then \
	if [ ! -f "$$manager" ] || [ ! -x "$$manager" ]; then \
		printf 'Carbonite host-build manager is not an executable file: %s\n' "$$manager" >&2; \
		exit 1; \
	fi; \
	"$$manager" prepare --gateway "$(CARBONITE_GATEWAY)" --workspace "$(CARBONITE_WORKSPACE)" \
		--sandbox "$(CARBONITE_NAME)" --checkout "$$(pwd -P)"; \
fi
endef

# Creation uses Carbonite's image/policy and an approved project volume.
# Image, mount, policy and provider changes take effect after rebuild-carb.
define ensure_carbonite
@set -eu; \
$(carbonite_state) \
case "$$phase" in \
	Ready) ;; \
	Stopped) $(CARBONITE_RUN) sandbox start "$(CARBONITE_NAME)" ;; \
	Absent) \
		if [ ! -f "$(CARBONITE_POLICY)" ]; then \
			printf 'Carbonite policy not found: %s\nSet CARBONITE_POLICY to the project policy.\n' "$(CARBONITE_POLICY)" >&2; \
			exit 1; \
		fi; \
		kube_dir="$${CARBONITE_KUBECONFIG_DIR:-}"; \
		if [ -n "$$kube_dir" ]; then \
			case "$$kube_dir" in /*) ;; *) printf 'CARBONITE_KUBECONFIG_DIR must be an absolute path.\n' >&2; exit 1 ;; esac; \
			case "$$kube_dir" in *:*) printf 'CARBONITE_KUBECONFIG_DIR cannot contain a colon.\n' >&2; exit 1 ;; esac; \
			if [ ! -d "$$kube_dir" ] || [ "$$kube_dir" != "$$(readlink -f -- "$$kube_dir")" ]; then \
				printf 'CARBONITE_KUBECONFIG_DIR must be an existing canonical directory without symlinks.\n' >&2; exit 1; \
			fi; \
			checkout="$$(pwd -P)"; \
			case "$$kube_dir" in "$$checkout"|"$$checkout"/*) \
				printf 'Keep the kubeconfig directory outside the sandbox-mounted checkout.\n' >&2; exit 1 ;; esac; \
			policy="$$(readlink -f -- "$(CARBONITE_POLICY)")"; \
			case "$$policy" in "$$checkout"|"$$checkout"/*) \
				printf 'For cluster access, set CARBONITE_POLICY to a host-only policy outside the checkout.\n' >&2; exit 1 ;; esac; \
			if ! $(CONTAINER_CMD) run --rm --pull=never --network none --read-only \
				--userns=keep-id:uid=1000,gid=1000 \
				--security-opt no-new-privileges --cap-drop ALL --entrypoint /bin/sh \
				--volume "$$kube_dir:/sandbox/kube:ro,z" "$(CARBONITE_IMAGE_REF)" \
				-c 'test -f /sandbox/kube/config && test ! -L /sandbox/kube/config && test -r /sandbox/kube/config && test ! -w /sandbox/kube/config && test -z "$$(find /sandbox/kube -maxdepth 0 -perm /077 -print -quit)" && test -z "$$(find /sandbox/kube/config -maxdepth 0 -perm /077 -print -quit)" && test -z "$$(find /sandbox/kube -mindepth 1 -maxdepth 1 ! -name config -print -quit)"'; then \
				printf 'Kubeconfig mount is not a private, readable, read-only config. Prepare its rootless ownership as described in the KiND guide.\n' >&2; exit 1; \
			fi; \
		fi; \
		$(CONTAINER_CMD) volume create --ignore \
			--label openshell.ai/sandbox-attachable=true \
			--label "openshell.ai/sandbox-attachable-workspace=$(CARBONITE_WORKSPACE)" \
			--opt type=none --opt o=bind --opt "device=$(CURDIR)" "$(CARBONITE_VOLUME)"; \
		volume="$$( $(CONTAINER_CMD) volume inspect "$(CARBONITE_VOLUME)" --format '{{json .}}' )"; \
		if ! printf '%s\n' "$$volume" | jq -e --arg source "$(CURDIR)" --arg workspace "$(CARBONITE_WORKSPACE)" \
			'.Options.device == $$source and .Options.type == "none" and .Options.o == "bind" and \
			 .Labels["openshell.ai/sandbox-attachable"] == "true" and \
			 .Labels["openshell.ai/sandbox-attachable-workspace"] == $$workspace' >/dev/null; then \
			printf 'Volume %s has a different source or approval labels; set CARBONITE_VOLUME to a new name.\n' "$(CARBONITE_VOLUME)" >&2; \
			exit 1; \
		fi; \
		if [ -n "$$kube_dir" ]; then \
			$(CONTAINER_CMD) volume create --ignore \
				--label openshell.ai/sandbox-attachable=true \
				--label "openshell.ai/sandbox-attachable-workspace=$(CARBONITE_WORKSPACE)" \
				--opt type=none --opt o=bind --opt "device=$$kube_dir" "$(CARBONITE_KUBECONFIG_VOLUME)"; \
			kube_volume="$$( $(CONTAINER_CMD) volume inspect "$(CARBONITE_KUBECONFIG_VOLUME)" --format '{{json .}}' )"; \
			if ! printf '%s\n' "$$kube_volume" | jq -e --arg source "$$kube_dir" --arg workspace "$(CARBONITE_WORKSPACE)" \
				'.Options.device == $$source and .Options.type == "none" and .Options.o == "bind" and \
				 .Labels["openshell.ai/sandbox-attachable"] == "true" and \
				 .Labels["openshell.ai/sandbox-attachable-workspace"] == $$workspace' >/dev/null; then \
				printf 'Kubeconfig volume has a different source or approval labels; inspect it before retrying.\n' >&2; \
				exit 1; \
			fi; \
		fi; \
		$(CONTAINER_CMD) run --rm --network none --read-only \
			--security-opt no-new-privileges --cap-drop ALL --entrypoint /bin/true \
			--volume "$(CURDIR):/project:z" "$(CARBONITE_IMAGE_REF)"; \
		mount_config="$$(jq -nc --arg source "$(CARBONITE_VOLUME)" --arg target "$(CARBONITE_WORKDIR)" \
			--arg kube_dir "$$kube_dir" --arg kube_volume "$(CARBONITE_KUBECONFIG_VOLUME)" \
			'{podman: {mounts: ([{type: "volume", source: $$source, target: $$target, read_only: false}] + \
				(if $$kube_dir == "" then [] else [{type: "volume", source: $$kube_volume, target: "/sandbox/kube", read_only: true}] end))}}')"; \
		if [ -n "$$kube_dir" ]; then set -- --env 'KUBECONFIG=/sandbox/kube/config'; else set --; fi; \
		$(CARBONITE_RUN) sandbox create --name "$(CARBONITE_NAME)" \
			--from "$(CARBONITE_IMAGE_REF)" --policy "$(CARBONITE_POLICY)" \
			--driver-config-json "$$mount_config" --env "PROJECT_DIR=$(CARBONITE_WORKDIR)" "$$@" \
			$(CARBONITE_CREATE_ARGS) --no-tty --detach --output json -- sleep infinity ;; \
	*) printf 'Sandbox %s is %s; inspect it with make status-carb.\n' "$(CARBONITE_NAME)" "$$phase" >&2; exit 1 ;; \
esac
endef

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

check-carb: ## Check host commands, container engine and gateway authentication
	$(call carbonite_require_commands,"$(CONTAINER_CMD)" "$(OPENSHELL_CMD)" jq)
	$(carbonite_check_engine)
	@set -eu; \
	if ! gateway_status="$$( $(CARBONITE_RUN) status --output json )"; then \
		printf 'OpenShell gateway check failed for "%s".\nCheck registration with: %s gateway list\nFor a local gateway, check: systemctl --user status openshell-gateway.service\nFor an OIDC gateway, authenticate with: %s gateway login "%s"\n' \
			"$(CARBONITE_GATEWAY)" "$(OPENSHELL_CMD)" "$(OPENSHELL_CMD)" "$(CARBONITE_GATEWAY)" >&2; \
		exit 1; \
	fi; \
	if ! printf '%s\n' "$$gateway_status" | jq -e \
		'.status == "connected" and .authentication.status == "authenticated"' >/dev/null; then \
		printf 'Gateway "%s" must be connected and authenticated.\nInspect with: %s --gateway "%s" status\nFor an OIDC gateway, authenticate with: %s gateway login "%s"\n' \
			"$(CARBONITE_GATEWAY)" "$(OPENSHELL_CMD)" "$(CARBONITE_GATEWAY)" "$(OPENSHELL_CMD)" "$(CARBONITE_GATEWAY)" >&2; \
		exit 1; \
	fi; \
	printf 'Carbonite prerequisites OK: gateway "%s" is connected and authenticated.\n' "$(CARBONITE_GATEWAY)"

image: image-$(DEV_CONTAINER_SUFFIX) ## Build the selected development-container image

image-distrobox:
	$(CONTAINER_CMD) build -t $(IMAGE_NAME) -f $(DISTROBOX_CONTEXT)/Containerfile $(DISTROBOX_CONTEXT)

image-carb: ## Build the carbonite image
	$(call carbonite_require_commands,"$(CONTAINER_CMD)")
	$(carbonite_check_engine)
	$(CONTAINER_CMD) build -t $(CARBONITE_IMAGE_NAME) -f $(CARBONITE_CONTEXT)/Containerfile $(CARBONITE_CONTEXT)

enter: enter-$(DEV_CONTAINER_SUFFIX) ## Enter the selected development container

enter-distrobox: image-distrobox
	@if ! distrobox list --no-color 2>/dev/null | awk -F'|' 'NR>1{gsub(/^ +| +$$/,"",$$2); print $$2}' | grep -Fxq "$(DISTROBOX_NAME)"; then \
		distrobox create --image $(IMAGE_NAME) --name $(DISTROBOX_NAME) --home "$(HOME_DIR)"; \
	fi
	distrobox enter $(DISTROBOX_NAME)

enter-carb: check-carb ## Enter the Carbonite/OpenShell sandbox (creates or starts it)
	$(MAKE) image-carb
	$(ensure_carbonite)
	$(carbonite_verify_kubeconfig)
	$(carbonite_prepare_host_build)
	$(CARBONITE_RUN) sandbox exec --name "$(CARBONITE_NAME)" --workdir "$(CARBONITE_WORKDIR)" --tty -- /bin/bash -il

claude: claude-$(DEV_CONTAINER_SUFFIX) ## Run Claude Code inside the selected container (ARGS="--flag" to pass flags)

claude-distrobox: image-distrobox
	@if ! distrobox list --no-color 2>/dev/null | awk -F'|' 'NR>1{gsub(/^ +| +$$/,"",$$2); print $$2}' | grep -Fxq "$(DISTROBOX_NAME)"; then \
		distrobox create --image $(IMAGE_NAME) --name $(DISTROBOX_NAME) --home "$(HOME_DIR)"; \
	fi
	distrobox enter $(DISTROBOX_NAME) -- claude $(ARGS)

claude-carb: check-carb ## Run Claude Code inside Carbonite (ARGS="--flag" to pass flags)
	$(MAKE) image-carb
	$(ensure_carbonite)
	$(carbonite_verify_kubeconfig)
	$(carbonite_prepare_host_build)
	$(CARBONITE_RUN) sandbox exec --name "$(CARBONITE_NAME)" --workdir "$(CARBONITE_WORKDIR)" --tty -- /bin/bash -lc 'exec claude "$$@"' bash $(ARGS)

stop: stop-$(DEV_CONTAINER_SUFFIX) ## Stop the selected development container

stop-distrobox:
	@distrobox stop -Y $(DISTROBOX_NAME) 2>/dev/null || true

stop-carb: check-carb ## Stop the Carbonite sandbox, retaining its workspace
	@set -eu; \
	$(carbonite_state) \
	case "$$phase" in \
		Absent|Stopped) ;; \
		*) $(CARBONITE_RUN) sandbox stop "$(CARBONITE_NAME)" ;; \
	esac

rm: rm-$(DEV_CONTAINER_SUFFIX) ## Remove the selected development container

rm-distrobox:
	@distrobox rm --force $(DISTROBOX_NAME) 2>/dev/null || true

rm-carb: check-carb ## Remove the Carbonite sandbox
	@set -eu; \
	$(carbonite_state) \
	if [ "$$phase" != Absent ]; then \
		$(CARBONITE_RUN) sandbox delete "$(CARBONITE_NAME)"; \
		for attempt in $$(seq 1 60); do \
			$(carbonite_state) \
			[ "$$phase" != Absent ] || exit 0; \
			sleep 1; \
		done; \
		printf 'Timed out waiting for sandbox %s to be removed.\n' "$(CARBONITE_NAME)" >&2; \
		exit 1; \
	fi

rebuild: rebuild-$(DEV_CONTAINER_SUFFIX) ## Rebuild and enter the selected development container

rebuild-distrobox: rm-distrobox image-distrobox enter-distrobox

rebuild-carb: check-carb ## Remove the Carbonite sandbox, rebuild its image and enter
	$(MAKE) rm-carb
	$(MAKE) enter-carb

status: status-$(DEV_CONTAINER_SUFFIX) ## Show the selected development-container status

status-distrobox:
	@echo "=== Images ==="
	@$(CONTAINER_CMD) images --filter reference=$(IMAGE_NAME) --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.Created}}"
	@echo ""
	@echo "=== Distrobox ==="
	@distrobox list --no-color 2>/dev/null | head -1; distrobox list --no-color 2>/dev/null | awk -F'|' 'NR>1{gsub(/^ +| +$$/,"",$$2); if($$2=="$(DISTROBOX_NAME)") { print; found=1 }} END { if (!found) print "  (not created)" }'

status-carb: check-carb ## Show Carbonite image and sandbox status
	@$(CONTAINER_CMD) images --filter reference=$(CARBONITE_IMAGE_NAME) --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.Created}}"
	@set -eu; \
	$(carbonite_state) \
	if [ "$$phase" = Absent ]; then \
		printf 'Sandbox %s: (not created)\n' "$(CARBONITE_NAME)"; \
	else \
		$(CARBONITE_RUN) sandbox get "$(CARBONITE_NAME)"; \
	fi
