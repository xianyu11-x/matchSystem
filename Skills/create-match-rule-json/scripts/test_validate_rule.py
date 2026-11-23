#!/usr/bin/env python3
"""规则身份的格式和兼容性回归检查。"""

import json
import unittest
from pathlib import Path

from validate_rule import MAX_UINT64, Validator, fingerprint


class RuleIdentityTests(unittest.TestCase):
    def setUp(self):
        asset = Path(__file__).resolve().parent.parent / "assets" / "default-rule.json"
        self.rule = json.loads(asset.read_text(encoding="utf-8"))

    def validate_key(self, key):
        document = dict(self.rule)
        document["ruleKey"] = key
        validator = Validator()
        validator.rule(document)
        return document, validator

    def test_full_uint64_identity(self):
        for key in (
            {"ruleId": 1},
            {"namespace": "", "ruleId": "1"},
            {"namespace": "demo", "ruleId": 1},
            {"namespace": "09", "ruleId": "1"},
            {"namespace": "展示别名", "ruleId": MAX_UINT64},
            {"namespace": str(MAX_UINT64 + 1), "ruleId": str(MAX_UINT64)},
        ):
            with self.subTest(key=key):
                _, validator = self.validate_key(key)
                self.assertEqual(validator.issues, [])

    def test_reject_out_of_range_and_noncanonical_identity(self):
        for key in (
            {"ruleId": 0}, {"ruleId": -1}, {"ruleId": "-1"},
            {"ruleId": True}, {"ruleId": "label"}, {"ruleId": MAX_UINT64 + 1},
            {"ruleId": ""}, {"ruleId": "01"}, {"ruleId": "+1"},
            {"ruleId": str(MAX_UINT64 + 1)},
            {"namespace": 0, "ruleId": 1}, {"namespace": True, "ruleId": 1},
            {"namespace": None, "ruleId": 1}, {"namespace": [], "ruleId": 1},
        ):
            with self.subTest(key=key):
                _, validator = self.validate_key(key)
                self.assertTrue(validator.issues)

    def test_fingerprint_normalizes_equivalent_rule_identity(self):
        fingerprints = []
        for key in ({"ruleId": 1}, {"namespace": "demo", "ruleId": 1},
                    {"namespace": "展示别名", "ruleId": "1"}, {"namespace": "01", "ruleId": 1}):
            with self.subTest(key=key):
                document, validator = self.validate_key(key)
                self.assertEqual(validator.issues, [])
                fingerprints.append(fingerprint(document, validator.rule_key))
        self.assertEqual(len(set(fingerprints)), 1)
        document, validator = self.validate_key({"namespace": "demo", "ruleId": 2})
        self.assertEqual(validator.issues, [])
        self.assertNotEqual(fingerprint(document, validator.rule_key), fingerprints[0])


if __name__ == "__main__":
    unittest.main()
