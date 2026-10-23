"""Offline OpenAPI 3.0 queries and JSON Schema validation for contract tests.

No HTTP requests, schema downloads, fixture generation, or backend calls.  Tests
supply independent, literal examples; this module only resolves local references
and translates the OpenAPI 3.0 nullable keyword for jsonschema's Draft 7 engine.
"""

import copy
import json
import os
import re
from pathlib import Path

import yaml
from jsonschema import Draft7Validator, FormatChecker


METHODS = frozenset({"get", "put", "post", "delete", "patch", "head", "options", "trace"})
DEFAULT_CONTRACT = Path(__file__).resolve().parents[1] / "openapi.yaml"


class UniqueKeyLoader(yaml.SafeLoader):
    """Fail on duplicate YAML mapping keys instead of silently losing an operation."""


def _unique_mapping(loader, node, deep=False):
    result = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in result:
            raise ValueError("duplicate YAML key {!r} on line {}".format(key, key_node.start_mark.line + 1))
        result[key] = loader.construct_object(value_node, deep=deep)
    return result


UniqueKeyLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, _unique_mapping)


def walk(value, path=()):
    """Walk raw mappings without resolving refs (safe for recursive schemas)."""
    if isinstance(value, dict):
        yield path, value
        for key, child in value.items():
            yield from walk(child, path + (str(key),))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from walk(child, path + (str(index),))


def canonical_path(path):
    return re.sub(r"\{[^}]+\}", "{}", path)


class Contract:
    def __init__(self, path=None):
        self.path = Path(path or os.environ.get("NERAE_OPENAPI", DEFAULT_CONTRACT)).resolve()
        # Runtime input is one local contract file, not any credentials or server.
        self.source = self.path.read_text(encoding="utf-8")
        self.doc = yaml.load(self.source, Loader=UniqueKeyLoader)
        if not isinstance(self.doc, dict):
            raise AssertionError("OpenAPI document must be a mapping")
        self._validators = {}

    def resolve(self, ref):
        if not isinstance(ref, str) or not ref.startswith("#/"):
            raise AssertionError("only local $ref is supported: {!r}".format(ref))
        value = self.doc
        for token in ref[2:].split("/"):
            token = token.replace("~1", "/").replace("~0", "~")
            try:
                value = value[token]
            except (KeyError, TypeError) as error:
                raise AssertionError("unresolved $ref: " + ref) from error
        return value

    def deref(self, value):
        visited = set()
        while isinstance(value, dict) and "$ref" in value:
            ref = value["$ref"]
            if ref in visited:
                raise AssertionError("cyclic Reference Object: " + ref)
            visited.add(ref)
            value = self.resolve(ref)
        return value

    def operations(self):
        for path, raw in self.doc.get("paths", {}).items():
            item = self.deref(raw)
            for method, operation in item.items():
                if method in METHODS:
                    yield path, method, self.deref(operation)

    def operation(self, path, method):
        matches = [op for actual, verb, op in self.operations()
                   if canonical_path(actual) == canonical_path(path) and verb == method.lower()]
        if len(matches) != 1:
            raise AssertionError("expected one operation {} {}, found {}".format(method.upper(), path, len(matches)))
        return matches[0]

    def request(self, path, method):
        operation = self.operation(path, method)
        body = self.deref(operation.get("requestBody", {}))
        if body.get("required") is not True:
            raise AssertionError("{} {} must require a request body".format(method.upper(), path))
        try:
            return body["content"]["application/json"]["schema"]
        except KeyError as error:
            raise AssertionError("missing application/json request schema: {} {}".format(method, path)) from error

    def response(self, path, method="get", status=None):
        responses = self.operation(path, method).get("responses", {})
        if status is not None:
            candidates = [value for code, value in responses.items() if str(code) == str(status)]
        else:
            candidates = [value for code, value in responses.items() if str(code).startswith("2")]
        for response in candidates:
            response = self.deref(response)
            schema = response.get("content", {}).get("application/json", {}).get("schema")
            if schema is not None:
                return schema
        raise AssertionError("missing JSON success response: {} {}".format(method.upper(), path))

    def parameters(self, path, method):
        actual = next((key for key in self.doc["paths"] if canonical_path(key) == canonical_path(path)), None)
        if actual is None:
            raise AssertionError("missing path: " + path)
        item = self.deref(self.doc["paths"][actual])
        combined = {}
        for raw in item.get("parameters", []) + self.operation(path, method).get("parameters", []):
            parameter = self.deref(raw)
            combined[(parameter["in"], parameter["name"].lower())] = parameter
        return list(combined.values())

    def schema(self, *names, predicate=None):
        """Prefer published aliases, then an unambiguous semantic lookup; never skip."""
        schemas = self.doc.get("components", {}).get("schemas", {})
        for name in names:
            if name in schemas:
                return {"$ref": "#/components/schemas/" + name}
        matches = [name for name, schema in schemas.items() if predicate and predicate(schema)]
        if len(matches) == 1:
            return {"$ref": "#/components/schemas/" + matches[0]}
        raise AssertionError("missing/ambiguous schema {} (semantic matches: {})".format(names, matches))

    def properties(self, schema):
        """Collect properties through allOf; does NOT collapse oneOf/anyOf branches."""
        schema = self.deref(schema)
        props = dict(schema.get("properties", {}))
        for part in schema.get("allOf", []):
            props.update(self.properties(part))
        return props

    def required(self, schema):
        schema = self.deref(schema)
        required = set(schema.get("required", []))
        for part in schema.get("allOf", []):
            required.update(self.required(part))
        return required

    def property(self, schema, *aliases):
        props = self.properties(schema)
        for alias in aliases:
            if alias in props:
                return props[alias]
        raise AssertionError("missing property (accepted aliases: {})".format(", ".join(aliases)))

    def reachable(self, schema, seen=None):
        """Walk schemas and local refs, including union branches, without looping."""
        seen = set() if seen is None else seen
        for path, node in walk(schema):
            yield path, node
            if "$ref" in node and node["$ref"] not in seen:
                seen.add(node["$ref"])
                yield from self.reachable(self.resolve(node["$ref"]), seen)

    def references(self, schema):
        return {node["$ref"] for _, node in self.reachable(schema) if "$ref" in node}

    def _json_schema(self, schema):
        """Translate only OAS 3.0 schema syntax, not imagined server behavior.

        readOnly/writeOnly stay annotations: dedicated tests check them explicitly.
        nullable is handled only when type is explicit, as OAS 3.0 requires.
        No allOf flattening or added additionalProperties/required constraints.
        """
        if isinstance(schema, list):
            return [self._json_schema(value) for value in schema]
        if not isinstance(schema, dict):
            return schema
        converted = {key: self._json_schema(value) for key, value in schema.items() if key != "nullable"}
        if schema.get("nullable") is True and "type" in schema:
            value_type = schema["type"]
            converted["type"] = list(dict.fromkeys(
                (value_type if isinstance(value_type, list) else [value_type]) + ["null"]))
        for keyword in ("exclusiveMinimum", "exclusiveMaximum"):
            if isinstance(schema.get(keyword), bool):
                bound = keyword.replace("exclusive", "", 1).lower()
                converted.pop(keyword, None)
                if schema[keyword] and bound in schema:
                    converted[keyword] = schema[bound]
                    converted.pop(bound, None)
        return converted

    def validator(self, schema):
        cache_key = json.dumps(schema, sort_keys=True)
        if cache_key in self._validators:
            return self._validators[cache_key]
        # A document-local root allows jsonschema to resolve #/components/... refs
        # without a deprecated RefResolver, any network, or any external registry.
        for _, node in self.reachable(schema):
            if "$ref" in node:
                self.resolve(node["$ref"])
        root = self._json_schema(copy.deepcopy(schema))
        root["components"] = self._json_schema(self.doc.get("components", {}))
        Draft7Validator.check_schema(root)
        self._validators[cache_key] = Draft7Validator(root, format_checker=FormatChecker())
        return self._validators[cache_key]

    def errors(self, schema, instance):
        return sorted(self.validator(schema).iter_errors(instance), key=lambda error: str(list(error.path)))

    def is_valid(self, schema, instance):
        return not self.errors(schema, instance)
