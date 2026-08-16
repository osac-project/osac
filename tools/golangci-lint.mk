# Shared golangci-lint installation for osac mono-repo components.
#
# Include this file from a component Makefile:
#   include $(shell git rev-parse --show-toplevel)/tools/golangci-lint.mk
# and reference $(GOLANGCI_LINT) from lint/lint-fix/lint-config targets (depend
# on the `golangci-lint` target to trigger the download). The binary installs
# once into a repo-root-level bin/ directory shared by every component,
# instead of each component downloading its own copy.

# Named GOLANGCI_LINT_BIN rather than LOCALBIN (the name component Makefiles
# use) to make clear this directory is shared across every component, not
# scoped to one.
GOLANGCI_LINT_BIN ?= $(shell git rev-parse --show-toplevel)/tools/bin
GOLANGCI_LINT = $(GOLANGCI_LINT_BIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.12.1

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
