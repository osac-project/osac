# Integration testing

## fulfillment-service

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Component integration | `it/`; `make -C ../osac-installer test SUITE=fulfillment` | Deployed Fulfillment Service and PostgreSQL | Provider lifecycles |
| E2E | `../tests/e2e/` | Cross-component journeys | None |

### Coverage notes

- **DiskImage API-only workflows ([DEV] OSAC-1111):** `it_disk_images_api_test.go` covers public CRUD for provider admin images. These are deployed API and database checks. Related test-plan cases: TC-FR1-01 and TC-FR1-02. Run them through the Fulfillment installer target.
- **Unrelated workflows:** `it_labels_test.go` covers reserved metadata. Related test-plan cases: TC-FR1-01.
- **DiskImage E2E journeys (OSAC-1111):** `tests/e2e/test_disk_image_create.py` runs the deployed journey. Related test-plan cases: TC-FR7-04.
