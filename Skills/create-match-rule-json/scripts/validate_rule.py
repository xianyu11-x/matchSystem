#!/usr/bin/env python3
"""Small standard-library-only format checker for match-rule/v1 JSON."""

from __future__ import annotations

import hashlib
import json
import math
import sys
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple

VERSIONS = {
    "rule": "match-rule/v1", "contract": "logical-node-contract/v3",
    "prefilter": "prefilter/v3", "evaluation": "evaluation/v3",
    "expression": "expression-scalar/v3",
}


class DuplicateKey(ValueError):
    pass


def object_pairs(pairs: List[Tuple[str, Any]]) -> Dict[str, Any]:
    value: Dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise DuplicateKey("duplicate object key %r" % key)
        value[key] = item
    return value


def reject_constant(value: str) -> None:
    raise ValueError("non-standard JSON number %s" % value)


class Validator:
    def __init__(self) -> None:
        self.issues: List[Dict[str, str]] = []

    def error(self, path: str, code: str, message: str) -> None:
        self.issues.append({"path": path, "code": code, "message": message})

    def obj(self, value: Any, path: str, required: Iterable[str] = (), allowed: Optional[Iterable[str]] = None) -> Optional[Dict[str, Any]]:
        if type(value) is not dict:
            self.error(path, "TYPE_MISMATCH", "object is required")
            return None
        allowed_set = set(allowed) if allowed is not None else None
        if allowed_set is not None:
            for key in sorted(value):
                if key not in allowed_set:
                    self.error(path + "." + key, "UNKNOWN_FIELD", "unknown field %r" % key)
        for key in required:
            if key not in value:
                self.error(path + "." + key, "MISSING_FIELD", "%s is required" % key)
            elif value[key] is None:
                self.error(path + "." + key, "NULL_NOT_ALLOWED", "%s must not be null" % key)
        return value

    def string(self, value: Any, path: str, non_empty: bool = False) -> Optional[str]:
        if type(value) is not str:
            self.error(path, "TYPE_MISMATCH", "string is required")
            return None
        if non_empty and not value:
            self.error(path, "INVALID_VALUE", "string must not be empty")
            return None
        return value

    def integer(self, value: Any, path: str, minimum: Optional[int] = None) -> Optional[int]:
        if type(value) is not int:
            self.error(path, "TYPE_MISMATCH", "integer is required")
            return None
        if minimum is not None and value < minimum:
            self.error(path, "INVALID_VALUE", "value must be at least %d" % minimum)
            return None
        return value

    def number(self, value: Any, path: str) -> Optional[float]:
        if type(value) not in (int, float):
            self.error(path, "TYPE_MISMATCH", "number is required")
            return None
        if not math.isfinite(value):
            self.error(path, "INVALID_VALUE", "number must be finite")
            return None
        return value

    def array(self, value: Any, path: str, minimum: Optional[int] = None) -> Optional[List[Any]]:
        if type(value) is not list:
            self.error(path, "TYPE_MISMATCH", "array is required")
            return None
        if minimum is not None and len(value) < minimum:
            self.error(path, "INVALID_VALUE", "array must contain at least %d item(s)" % minimum)
        return value

    def enum(self, value: Any, path: str, values: Iterable[str]) -> Optional[str]:
        parsed = self.string(value, path)
        if parsed is not None and parsed not in values:
            self.error(path, "INVALID_VALUE", "unsupported value %r" % parsed)
            return None
        return parsed

    def version(self, value: Any, path: str, expected: str) -> None:
        parsed = self.string(value, path)
        if parsed is not None and parsed != expected:
            self.error(path, "UNKNOWN_SCHEMA_VERSION", "expected %r" % expected)

    def rule(self, value: Any) -> None:
        fields = ("schemaVersion", "ruleKey", "contract", "prefilter", "evaluation", "scoring", "seedSelection", "runtime")
        obj = self.obj(value, "$", fields, fields)
        if obj is None:
            return
        self.version(obj.get("schemaVersion"), "$.schemaVersion", VERSIONS["rule"])
        key = self.obj(obj.get("ruleKey"), "$.ruleKey", ("ruleId",), ("namespace", "ruleId"))
        if key is not None:
            if "namespace" in key:
                self.string(key["namespace"], "$.ruleKey.namespace")
            self.integer(key.get("ruleId"), "$.ruleKey.ruleId", 1)
        self.contract(obj.get("contract")); self.prefilter(obj.get("prefilter"))
        self.evaluation(obj.get("evaluation")); self.scoring(obj.get("scoring"))
        self.seed(obj.get("seedSelection")); self.runtime(obj.get("runtime"))

    def contract(self, value: Any) -> None:
        fields = ("schemaVersion", "attributes", "facts", "indexes")
        obj = self.obj(value, "$.contract", fields, fields + ("limits",))
        if obj is None:
            return
        self.version(obj.get("schemaVersion"), "$.contract.schemaVersion", VERSIONS["contract"])
        for name, scoped in (("attributes", False), ("facts", True)):
            path = "$.contract." + name
            items = self.array(obj.get(name), path)
            if items is None:
                continue
            for i, item in enumerate(items):
                p = "%s[%d]" % (path, i)
                entry = self.obj(item, p, ("name", "type"), ("name", "type", "scope", "maxValues"))
                if entry is None:
                    continue
                kind = self.enum(entry.get("type"), p + ".type", ("strings", "uint64s", "int64"))
                self.string(entry.get("name"), p + ".name", True)
                required = ["name", "type"]
                if scoped:
                    required.append("scope"); self.enum(entry.get("scope"), p + ".scope", ("tick", "object", "match"))
                if kind in ("strings", "uint64s"):
                    required.append("maxValues"); self.integer(entry.get("maxValues"), p + ".maxValues", 1)
                self.obj(entry, p, required, required)
        path = "$.contract.indexes"; items = self.array(obj.get("indexes"), path)
        if items is not None:
            for i, item in enumerate(items):
                p = "%s[%d]" % (path, i)
                entry = self.obj(item, p, ("type", "name"), ("type", "name", "keyType", "maxDocumentValues", "maxQueryValues"))
                if entry is None:
                    continue
                kind = self.enum(entry.get("type"), p + ".type", ("multi_value", "int64_range")); self.string(entry.get("name"), p + ".name", True)
                required = ["type", "name"]
                if kind == "multi_value":
                    required += ["keyType", "maxDocumentValues", "maxQueryValues"]
                    self.enum(entry.get("keyType"), p + ".keyType", ("string", "uint64"))
                    self.integer(entry.get("maxDocumentValues"), p + ".maxDocumentValues", 1); self.integer(entry.get("maxQueryValues"), p + ".maxQueryValues", 1)
                self.obj(entry, p, required, required)
        if "limits" in obj:
            names = ("maxBytes", "maxDepth", "maxChildren", "maxStringBytes", "maxIndexes", "maxAttributes", "maxFacts", "maxValues", "maxDocumentValues", "maxQueryValues")
            limits = self.obj(obj["limits"], "$.contract.limits", allowed=names)
            if limits is not None:
                for name in names:
                    if name in limits: self.integer(limits[name], "$.contract.limits." + name, 0)

    def prefilter(self, value: Any) -> None:
        obj = self.obj(value, "$.prefilter", ("schemaVersion", "bitmap"), ("schemaVersion", "bitmap", "runtime"))
        if obj is None: return
        self.version(obj.get("schemaVersion"), "$.prefilter.schemaVersion", VERSIONS["prefilter"])
        bitmap = self.obj(obj.get("bitmap"), "$.prefilter.bitmap", ("resultType", "expr"), ("resultType", "expr"))
        if bitmap is not None:
            self.enum(bitmap.get("resultType"), "$.prefilter.bitmap.resultType", ("bitmap",)); self.bitmap_expr(bitmap.get("expr"), "$.prefilter.bitmap.expr")
        if "runtime" in obj:
            runtime = self.obj(obj["runtime"], "$.prefilter.runtime", allowed=("containsProbeThreshold",))
            if runtime is not None and "containsProbeThreshold" in runtime: self.integer(runtime["containsProbeThreshold"], "$.prefilter.runtime.containsProbeThreshold", 0)

    def bitmap_expr(self, value: Any, path: str) -> None:
        fields = ("op", "children", "value", "when", "then", "else", "index", "values", "min", "max")
        obj = self.obj(value, path, ("op",), fields)
        if obj is None: return
        op = self.enum(obj.get("op"), path + ".op", ("none", "and", "or", "exclude", "if", "lookup_string", "lookup_uint64", "lookup_range"))
        required = ["op"]
        if op in ("and", "or"):
            required += ["children"]; children = self.array(obj.get("children"), path + ".children", 1)
            if children is not None:
                for i, child in enumerate(children): self.obj(child, "%s.children[%d]" % (path, i), ("op",))
        elif op == "exclude": required += ["value"]; self.obj(obj.get("value"), path + ".value", ("op",))
        elif op == "if":
            required += ["when", "then", "else"]; self.expression(obj.get("when"), path + ".when", "bool"); self.obj(obj.get("then"), path + ".then", ("op",)); self.obj(obj.get("else"), path + ".else", ("op",))
        elif op in ("lookup_string", "lookup_uint64", "lookup_range"):
            required += ["index"]; self.string(obj.get("index"), path + ".index", True)
            if op == "lookup_range": required += ["min", "max"]; self.expression(obj.get("min"), path + ".min", "int64"); self.expression(obj.get("max"), path + ".max", "int64")
            else: required += ["values"]; self.expression(obj.get("values"), path + ".values", "strings" if op == "lookup_string" else "uint64s")
        self.obj(obj, path, required, required)

    def expression(self, value: Any, path: str, expected: Optional[str] = None) -> None:
        fields = ("schemaVersion", "resultType", "expr")
        obj = self.obj(value, path, fields, fields)
        if obj is None: return
        self.version(obj.get("schemaVersion"), path + ".schemaVersion", VERSIONS["expression"])
        result = self.enum(obj.get("resultType"), path + ".resultType", ("bool", "int64", "strings", "uint64s"))
        if expected is not None and result is not None and result != expected: self.error(path + ".resultType", "TYPE_MISMATCH", "resultType must be %r" % expected)
        expr = self.obj(obj.get("expr"), path + ".expr", ("op",))
        if expr is not None: self.string(expr.get("op"), path + ".expr.op", True)

    def evaluation(self, value: Any) -> None:
        fields = ("schemaVersion", "canJoin", "canComplete"); obj = self.obj(value, "$.evaluation", fields, fields)
        if obj is None: return
        self.version(obj.get("schemaVersion"), "$.evaluation.schemaVersion", VERSIONS["evaluation"]); self.expression(obj.get("canJoin"), "$.evaluation.canJoin", "bool"); self.expression(obj.get("canComplete"), "$.evaluation.canComplete", "bool")

    def scoring(self, value: Any) -> None:
        obj = self.obj(value, "$.scoring", ("type", "params"), ("type", "params"))
        if obj is None: return
        kind = self.enum(obj.get("type"), "$.scoring.type", ("constant", "created_at", "int64_field")); path = "$.scoring.params"
        params = self.obj(obj.get("params"), path, allowed=("value", "field", "direction", "weight", "missingScore"))
        if params is None or kind is None: return
        required = ["value"] if kind == "constant" else ["direction"] if kind == "created_at" else ["field", "direction"]
        allowed = required + ([] if kind == "constant" else ["weight"] if kind == "created_at" else ["weight", "missingScore"])
        if "field" in params: self.string(params["field"], path + ".field", True)
        if "direction" in params: self.enum(params["direction"], path + ".direction", ("ascending", "descending"))
        for name in ("value", "weight", "missingScore"):
            if name in params: self.number(params[name], path + "." + name)
        if "weight" in params and type(params["weight"]) in (int, float) and params["weight"] <= 0: self.error(path + ".weight", "INVALID_VALUE", "weight must be greater than zero")
        self.obj(params, path, required, allowed)

    def seed(self, value: Any) -> None:
        obj = self.obj(value, "$.seedSelection", ("type", "params"), ("type", "params"))
        if obj is None: return
        kind = self.enum(obj.get("type"), "$.seedSelection.type", ("arrival", "oldest", "int64_priority", "random")); path = "$.seedSelection.params"
        params = self.obj(obj.get("params"), path, allowed=("field", "direction", "randomSeed"))
        if params is None or kind is None: return
        required = [] if kind in ("arrival", "oldest") else ["field", "direction"] if kind == "int64_priority" else ["randomSeed"]
        if "field" in params: self.string(params["field"], path + ".field", True)
        if "direction" in params: self.enum(params["direction"], path + ".direction", ("ascending", "descending"))
        if "randomSeed" in params: self.integer(params["randomSeed"], path + ".randomSeed")
        self.obj(params, path, required, required)

    def runtime(self, value: Any) -> None:
        fields = ("candidateLimitPerSeed", "maxPlayers", "attemptLimitPerProduceMatch", "attemptLimitPerMatchRound"); obj = self.obj(value, "$.runtime", fields, fields)
        if obj is None: return
        parsed = {name: self.integer(obj.get(name), "$.runtime." + name, 1) for name in fields}
        if parsed[fields[2]] is not None and parsed[fields[3]] is not None and parsed[fields[2]] > parsed[fields[3]]: self.error("$.runtime." + fields[2], "INVALID_VALUE", "attempt limit must not exceed the round limit")


def main(argv: Sequence[str]) -> int:
    if len(argv) != 2:
        print("usage: python validate_rule.py <rule-json-path>", file=sys.stderr); return 2
    try:
        value = json.loads(Path(argv[1]).read_bytes().decode("utf-8"), object_pairs_hook=object_pairs, parse_constant=reject_constant)
    except OSError as error:
        print("read rule JSON: %s" % error, file=sys.stderr); return 2
    except (UnicodeDecodeError, DuplicateKey, json.JSONDecodeError, ValueError) as error:
        report = {"valid": False, "issues": [{"path": "$", "code": "INVALID_JSON", "message": str(error)}]}; json.dump(report, sys.stdout, ensure_ascii=False, indent=2); print(); return 1
    validator = Validator(); validator.rule(value); report: Dict[str, Any] = {"valid": not validator.issues}
    if validator.issues:
        report["issues"] = validator.issues
    else:
        canonical = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8"); report["fingerprint"] = hashlib.sha256(canonical).hexdigest()
    json.dump(report, sys.stdout, ensure_ascii=False, indent=2); print(); return 0 if report["valid"] else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
