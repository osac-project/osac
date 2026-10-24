"""Contract tests for canonical serialization."""

import json
import unittest

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.serialize import (
    VOLATILE_FIELDS,
    canonical_digest,
    canonical_json,
    compact_json,
)


class TestCanonicalJson(unittest.TestCase):
    def test_keys_are_sorted_and_output_ends_with_one_newline(self):
        text = canonical_json({"b": 1, "a": 2})

        self.assertTrue(text.endswith("\n"))
        self.assertFalse(text.endswith("\n\n"))
        self.assertLess(text.index('"a"'), text.index('"b"'))

    def test_round_trips(self):
        value = {
            "z": [3, 1, 2],
            "a": {"nested": True, "none": None},
            "message": "non-ascii é stays literal",
        }
        self.assertEqual(json.loads(canonical_json(value)), value)

    def test_digest_is_stable_and_order_independent(self):
        first = canonical_digest({"b": 1, "a": 2})
        second = canonical_digest({"a": 2, "b": 1})

        self.assertEqual(first, second)
        self.assertRegex(first, r"^[0-9a-f]{64}$")

    def test_digest_changes_with_content(self):
        self.assertNotEqual(canonical_digest({"a": 1}), canonical_digest({"a": 2}))

    def test_compact_json_is_sorted_and_whitespace_free(self):
        self.assertEqual(compact_json({"b": 1, "a": [2, 3]}), '{"a":[2,3],"b":1}')

    def test_generated_at_is_the_only_documented_volatile_field(self):
        self.assertEqual(VOLATILE_FIELDS, ("generated_at",))


if __name__ == "__main__":
    unittest.main()
