#!/usr/bin/env python3
"""规则身份的格式和兼容性回归检查。"""

import copy
import json
import subprocess
import sys
import tempfile
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


class RuleFormatTests(unittest.TestCase):
    def setUp(self):
        asset = Path(__file__).resolve().parent.parent / "assets" / "default-rule.json"
        self.rule = json.loads(asset.read_text(encoding="utf-8"))

    def validate(self, document):
        validator = Validator()
        validator.rule(document)
        return validator.issues

    def assert_issue(self, issues, path, code):
        self.assertTrue(any(issue["path"] == path and issue["code"] == code for issue in issues), issues)

    def nearest(self):
        return {
            "op": "lookup_nearest",
            "index": "createTime",
            "target": {
                "schemaVersion": "expression-scalar/v3",
                "resultType": "int64",
                "expr": {"op": "int64_ref", "source": "seed_attributes", "name": "createTime"},
            },
            "direction": "right",
            "limit": 20,
        }

    def document_with_expr(self, expr):
        document = copy.deepcopy(self.rule)
        document["prefilter"]["bitmap"]["expr"] = expr
        return document

    def test_real_fifo_examples(self):
        names = ("fifo-match-rule.json", "fifo-8-match-rule.json")
        # 仓库副本可从脚本路径定位；安装副本在仓库根目录运行时使用当前目录。
        roots = (Path.cwd(), *Path(__file__).resolve().parents)
        example_dir = next((root / "examples" / "simulator" for root in roots
                            if all((root / "examples" / "simulator" / name).is_file() for name in names)), None)
        if example_dir is None:
            self.skipTest("当前环境没有仓库中的 FIFO 示例")
        for name, players in zip(names, (4, 8)):
            with self.subTest(example=name):
                document = json.loads((example_dir / name).read_text(encoding="utf-8"))
                self.assertEqual(document["runtime"]["maxPlayers"], players)
                self.assertEqual(self.validate(document), [])

    def test_default_rule_remains_a_valid_noop(self):
        self.assertEqual(self.validate(self.rule), [])
        self.assertEqual(self.rule["prefilter"]["bitmap"]["expr"], {"op": "none"})
        for name in ("canJoin", "canComplete"):
            self.assertEqual(self.rule["evaluation"][name]["expr"], {"op": "bool_literal", "value": False})

    def test_builtin_references_and_range_index_are_allowed(self):
        document = self.document_with_expr(self.nearest())
        document["contract"]["indexes"] = [{"type": "int64_range", "name": "createTime"}]
        document["evaluation"]["canJoin"]["expr"] = {
            "op": "int64_gte",
            "left": {"op": "int64_ref", "source": "candidate_attributes", "name": "createTime"},
            "right": {"op": "int64_ref", "source": "seed_attributes", "name": "createTime"},
        }
        document["scoring"] = {"type": "int64_field", "params": {"field": "createTime", "direction": "ascending"}}
        document["seedSelection"] = {"type": "int64_priority", "params": {"field": "createTime", "direction": "ascending"}}
        self.assertEqual(self.validate(document), [])

    def test_reject_explicit_reserved_attribute_and_fact(self):
        for section, entry in (
            ("attributes", {"name": "createTime", "type": "int64"}),
            ("attributes", {"name": "createTime", "type": "strings", "maxValues": 1}),
            ("facts", {"name": "createTime", "type": "int64", "scope": "tick"}),
            ("facts", {"name": "createTime", "type": "uint64s", "scope": "match", "maxValues": 1}),
        ):
            with self.subTest(section=section, entry=entry):
                document = copy.deepcopy(self.rule)
                document["contract"][section].append(entry)
                self.assert_issue(self.validate(document), "$.contract.%s[0].name" % section, "RESERVED_ATTRIBUTE")

        document = copy.deepcopy(self.rule)
        document["contract"]["attributes"] = [{"name": "createtime", "type": "int64"}]
        self.assertEqual(self.validate(document), [])

    def test_nearest_direction_and_uint64_bounds(self):
        for direction in ("left", "right", "both"):
            for limit in (1, MAX_UINT64):
                with self.subTest(direction=direction, limit=limit):
                    expr = self.nearest()
                    expr.update(direction=direction, limit=limit)
                    self.assertEqual(self.validate(self.document_with_expr(expr)), [])
        for direction in ("", "ascending", "LEFT", 1, True, None):
            with self.subTest(direction=direction):
                expr = self.nearest()
                expr["direction"] = direction
                issues = self.validate(self.document_with_expr(expr))
                self.assertTrue(any(issue["path"] == "$.prefilter.bitmap.expr.direction" for issue in issues), issues)
        for limit in (0, -1, MAX_UINT64 + 1, True, False, 1.0, "1", None):
            with self.subTest(limit=limit):
                expr = self.nearest()
                expr["limit"] = limit
                issues = self.validate(self.document_with_expr(expr))
                self.assertTrue(any(issue["path"] == "$.prefilter.bitmap.expr.limit" for issue in issues), issues)

    def test_nearest_requires_exact_fields_and_int64_target(self):
        prefix = "$.prefilter.bitmap.expr"
        for field in ("index", "target", "direction", "limit"):
            with self.subTest(missing=field):
                expr = self.nearest()
                del expr[field]
                self.assert_issue(self.validate(self.document_with_expr(expr)), prefix + "." + field, "MISSING_FIELD")
        for field, value, suffix, code in (
            ("index", "", ".index", "INVALID_VALUE"),
            ("target", None, ".target", "NULL_NOT_ALLOWED"),
            ("target", {"schemaVersion": "expression-scalar/v3", "resultType": "bool", "expr": {"op": "bool_literal"}}, ".target.resultType", "TYPE_MISMATCH"),
            ("target", {"schemaVersion": "expression-scalar/v2", "resultType": "int64", "expr": {"op": "int64_literal"}}, ".target.schemaVersion", "UNKNOWN_SCHEMA_VERSION"),
            ("target", {"schemaVersion": "expression-scalar/v3", "resultType": "int64", "expr": {}}, ".target.expr.op", "MISSING_FIELD"),
            ("values", {}, ".values", "UNKNOWN_FIELD"),
            ("unexpected", 1, ".unexpected", "UNKNOWN_FIELD"),
        ):
            with self.subTest(field=field, value=value):
                expr = self.nearest()
                expr[field] = value
                self.assert_issue(self.validate(self.document_with_expr(expr)), prefix + suffix, code)

    def test_nested_nearest_nodes_are_checked(self):
        invalid = self.nearest()
        invalid["limit"] = 0
        predicate = {"schemaVersion": "expression-scalar/v3", "resultType": "bool", "expr": {"op": "bool_literal", "value": True}}
        none = {"op": "none"}
        cases = (
            ({"op": "and", "children": [none, invalid]}, ".children[1]"),
            ({"op": "or", "children": [invalid]}, ".children[0]"),
            ({"op": "exclude", "value": invalid}, ".value"),
            ({"op": "if", "when": predicate, "then": invalid, "else": none}, ".then"),
            ({"op": "if", "when": predicate, "then": none, "else": invalid}, ".else"),
            ({"op": "and", "children": [{"op": "or", "children": [{"op": "exclude", "value": invalid}]}]}, ".children[0].children[0].value"),
        )
        for expr, suffix in cases:
            with self.subTest(suffix=suffix):
                self.assert_issue(self.validate(self.document_with_expr(expr)), "$.prefilter.bitmap.expr" + suffix + ".limit", "INVALID_VALUE")
                invalid["limit"] = 1
                try:
                    self.assertEqual(self.validate(self.document_with_expr(expr)), [])
                finally:
                    invalid["limit"] = 0

    def test_scalar_compilation_and_index_binding_remain_outside_format_check(self):
        expr = self.nearest()
        expr["index"] = "undeclared"
        expr["target"]["expr"] = {"op": "host_checks_this_scalar"}
        self.assertEqual(self.validate(self.document_with_expr(expr)), [])

    def test_cli_reports_valid_and_invalid_rules(self):
        script = Path(__file__).resolve().parent / "validate_rule.py"
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "rule.json"
            for valid in (True, False):
                with self.subTest(valid=valid):
                    expr = self.nearest()
                    if not valid:
                        expr["limit"] = 0
                    path.write_text(json.dumps(self.document_with_expr(expr)), encoding="utf-8")
                    result = subprocess.run([sys.executable, str(script), str(path)], capture_output=True, text=True, check=False)
                    self.assertEqual(result.returncode, 0 if valid else 1, result.stderr)
                    report = json.loads(result.stdout)
                    self.assertEqual(report["valid"], valid)
                    if valid:
                        self.assertEqual(len(report["fingerprint"]), 64)
                    else:
                        self.assert_issue(report["issues"], "$.prefilter.bitmap.expr.limit", "INVALID_VALUE")


if __name__ == "__main__":
    unittest.main()
