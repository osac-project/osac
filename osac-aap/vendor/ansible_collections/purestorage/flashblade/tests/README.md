# FlashBlade Collection - Test Suite

This directory contains the test suite for the Pure Storage FlashBlade Ansible Collection.

## Directory Structure

```
tests/
├── unit/                           # Unit tests
│   └── plugins/
│       ├── modules/                # Module unit tests
│       └── module_utils/           # Utility function unit tests
│           ├── test_time_utils.py  # Time conversion tests
│           └── test_common.py      # Common utilities tests
├── integration/                    # Integration tests
│   └── targets/                    # Integration test targets
├── conftest.py                     # Shared pytest fixtures
├── requirements.txt                # Test dependencies
└── README.md                       # This file
```

## Running Tests

### Prerequisites

Install test dependencies:

```bash
pip install -r tests/requirements.txt
```

### Run All Tests

```bash
pytest
```

### Run Specific Test Files

```bash
# Run time_utils tests
pytest tests/unit/plugins/module_utils/test_time_utils.py

# Run common utilities tests
pytest tests/unit/plugins/module_utils/test_common.py
```

### Run with Coverage

```bash
# Generate coverage report
pytest --cov=plugins --cov-report=html --cov-report=term

# View HTML coverage report
open htmlcov/index.html  # macOS/Linux
start htmlcov/index.html  # Windows
```

### Run Specific Test Classes or Methods

```bash
# Run specific test class
pytest tests/unit/plugins/module_utils/test_time_utils.py::TestTimeToMilliseconds

# Run specific test method
pytest tests/unit/plugins/module_utils/test_time_utils.py::TestTimeToMilliseconds::test_12hour_am_midnight
```

## Test Markers

Tests can be marked with the following markers:

- `@pytest.mark.unit` - Unit tests (fast, no external dependencies)
- `@pytest.mark.integration` - Integration tests (require FlashBlade access)
- `@pytest.mark.slow` - Slow-running tests

Run tests by marker:

```bash
# Run only unit tests
pytest -m unit

# Skip slow tests
pytest -m "not slow"
```

## Writing Tests

### Test File Naming

- Test files must start with `test_`
- Test classes must start with `Test`
- Test methods must start with `test_`

### Using Fixtures

Common fixtures are defined in `conftest.py`:

```python
def test_example(mock_module, mock_blade):
    """Test using shared fixtures."""
    # mock_module provides a mock Ansible module
    # mock_blade provides a mock FlashBlade client
    pass
```

### Example Test

```python
import pytest
from plugins.module_utils.time_utils import time_to_milliseconds, TimeConversionError

class TestTimeConversion:
    def test_valid_conversion(self):
        """Test valid time conversion."""
        assert time_to_milliseconds("2AM") == 7200000

    def test_invalid_format(self):
        """Test that invalid format raises error."""
        with pytest.raises(TimeConversionError):
            time_to_milliseconds("invalid")
```

## Coverage Goals

- **Current Coverage**: ~15% (time_utils + common utilities)
- **Short-term Goal**: 50% coverage (3 months)
- **Long-term Goal**: 80%+ coverage

### Priority Areas for Testing

1. ✅ `time_utils.py` - 100% coverage
2. ✅ `common.py` - ~80% coverage
3. ⏳ `purefb.py` - Core connection logic
4. ⏳ Critical modules (purefb_fs, purefb_bucket, etc.)

## CI/CD Integration

Tests are automatically run in GitHub Actions on:
- Pull requests
- Pushes to master
- Daily schedule

See `.github/workflows/main.yml` for CI configuration.

## Troubleshooting

### Import Errors

If you get import errors, ensure you're running pytest from the repository root:

```bash
cd /path/to/FlashBlade-Collection
pytest
```

### Missing Dependencies

Install all test dependencies:

```bash
pip install -r tests/requirements.txt
```

## Contributing

When adding new functionality:

1. Write tests first (TDD approach recommended)
2. Ensure tests pass: `pytest`
3. Check coverage: `pytest --cov=plugins`
4. Aim for 80%+ coverage on new code

## Resources

- [pytest documentation](https://docs.pytest.org/)
- [pytest-cov documentation](https://pytest-cov.readthedocs.io/)
- [Ansible Testing Guide](https://docs.ansible.com/projects/ansible/latest/dev_guide/testing.html)

