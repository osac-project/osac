# Single definition of "running inside an OpenShell sandbox".
#
# The host helpers (tools/carbonite/*.sh) treat a non-empty OPENSHELL_SANDBOX as
# "inside the sandbox". Plain `ifdef` and `ifneq ($(origin ...),undefined)`
# disagree when the variable is set to the empty string: `ifdef` is false,
# `origin` is not "undefined", so component Makefiles took opposite branches
# for the same environment. Empty means "not in a sandbox", as the helpers do.
OPENSHELL_SANDBOX_ACTIVE := $(if $(strip $(OPENSHELL_SANDBOX)),true,false)
