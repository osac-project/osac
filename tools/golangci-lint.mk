# Shared golangci-lint installation for osac mono-repo components.
#
# Include this file from a component Makefile (git-free, so it also works in
# the Carbonite sandbox, whose worktree .git points to a host-only gitdir):
#   OSAC_REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)
#   include $(OSAC_REPO_ROOT)/tools/golangci-lint.mk
# and reference $(GOLANGCI_LINT) from lint/lint-fix/lint-config targets (depend
# on the `golangci-lint` target to trigger the download). The binary installs
# once into a repo-root-level bin/ directory shared by every component,
# instead of each component downloading its own copy.

# Named GOLANGCI_LINT_BIN rather than LOCALBIN (the name component Makefiles
# use) to make clear this directory is shared across every component, not
# scoped to one. The tools directory is captured with := because
# MAKEFILE_LIST's last word is this file only while it is being parsed;
# GOLANGCI_LINT_BIN expands lazily.
GOLANGCI_LINT_TOOLS_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
GOLANGCI_LINT_BIN ?= $(GOLANGCI_LINT_TOOLS_DIR)/bin
GOLANGCI_LINT = $(GOLANGCI_LINT_BIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.13.2

$(GOLANGCI_LINT_BIN):
	mkdir -p $(GOLANGCI_LINT_BIN)

# FORCE (rather than a plain file prerequisite) makes this recipe run on every
# invocation, so a GOLANGCI_LINT_VERSION change always re-checks/re-links —
# otherwise, once the $(GOLANGCI_LINT) symlink exists, make would consider it
# up to date against $(GOLANGCI_LINT_BIN) forever and never revisit it.
.PHONY: golangci-lint FORCE
FORCE:

golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): FORCE | $(GOLANGCI_LINT_BIN)
	@[ -f "$(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION)" ] || { \
	set -e; \
	package=github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) ;\
	echo "Downloading $${package}" ;\
	rm -f $(GOLANGCI_LINT) || true ;\
	GOBIN=$(GOLANGCI_LINT_BIN) go install $${package} ;\
	mv $(GOLANGCI_LINT) $(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION) ;\
	} ;\
	ln -sf $(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION) $(GOLANGCI_LINT)
