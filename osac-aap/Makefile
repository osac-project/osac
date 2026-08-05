# Integration tests for osac.workflows collection
# Note: Must be run from repository root directory

.PHONY: test lint

test:
	@echo "=== Setting up test environment ==="
	cd tests/integration && ./setup_test_env.sh
	@echo ""
	@echo "=== Running integration tests ==="
	cd tests/integration && ./run_tests.sh
	@echo ""
	@echo "=== Tearing down test environment ==="
	cd tests/integration && ./teardown_test_env.sh

lint:
	uv run ansible-lint
