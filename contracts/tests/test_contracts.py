"""Validate public contracts offline, including invalid version/time/unknown fields."""

import copy
import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator, FormatChecker

ROOT = Path(__file__).resolve().parents[1]
NAMES = ["task-spec", "runtime-event", "agent-sync-request", "agent-sync-response"]


@pytest.mark.parametrize("name", NAMES)
def test_examples_match_versioned_schema(name: str) -> None:
    schema = json.loads((ROOT / "schemas" / f"{name}.schema.json").read_text(encoding="utf-8"))
    example = json.loads((ROOT / "examples" / f"{name}.example.json").read_text(encoding="utf-8"))
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    validator.validate(example)
    for field, value in [("schemaVersion", "2.0"), ("unexpectedField", True)]:
        invalid = copy.deepcopy(example)
        invalid[field] = value
        assert not validator.is_valid(invalid)
    for field in schema["required"]:
        invalid = copy.deepcopy(example)
        del invalid[field]
        assert not validator.is_valid(invalid)
    for field, definition in schema["properties"].items():
        if definition.get("format") == "date-time":
            invalid = copy.deepcopy(example)
            invalid[field] = "not-a-time"
            assert not validator.is_valid(invalid)
