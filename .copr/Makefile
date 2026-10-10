#
# Copyright (c) 2025 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
# License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
# language governing permissions and limitations under the License.
#

# Root-level wrapper so Copr can find .copr/Makefile at the top of the
# monorepo. Copr runs the target from the configured source subdirectory, so
# resolve the repository root from this Makefile instead of the working dir.
#
#   make -f .copr/Makefile srpm outdir=<results>
#
# This simply delegates to the fulfillment-service SRPM build script.

repo_root := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)

.PHONY: srpm
srpm:
	outdir="$(outdir)" "$(repo_root)/fulfillment-service/.copr/srpm.sh"
