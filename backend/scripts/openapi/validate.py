#!/usr/bin/env python3
"""Internal OpenAPI sanity gate for Pharmaciano's split-YAML contract tree.

Performs (1) YAML parse of every file, (2) cross-file `$ref` resolution with
JSON-pointer semantics, (3) a few structural checks (root has info+paths+components,
openapi version). Redocly CLI is the authoritative linter; this script is the
fast, dependency-free pre-gate used by `make oapi-check` and CI so feedback
cycles are seconds, not a full toolchain install.

Exit code 0 = clean; 1 = any failure.
"""
import os
import sys

try:
    import yaml  # PyYAML
except ImportError:
    print("error: PyYAML not installed (pip install --break-system-packages pyyaml)")
    sys.exit(1)

ROOT_ENV = os.environ.get("OPENAPI_ROOT", "")


def main() -> int:
    root = ROOT_ENV or os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "api", "openapi")
    root = os.path.abspath(root)

    parsed = {}
    files = []
    for r, _, fs in os.walk(root):
        for f in fs:
            if f.endswith(".yaml"):
                p = os.path.normpath(os.path.join(r, f))
                files.append(p)
    files.sort()

    errors = []
    for f in files:
        try:
            with open(f, encoding="utf-8") as fh:
                parsed[f] = yaml.safe_load(fh)
        except Exception as exc:  # noqa: BLE001
            errors.append(f"{os.path.relpath(f, root)}: YAML parse: {exc}")

    # -- structural checks on the root spec ----------------------------------
    root_spec = os.path.normpath(os.path.join(root, "openapi.yaml"))
    if root_spec in parsed:
        doc = parsed[root_spec]
        ver = str(doc.get("openapi", ""))
        if not ver.startswith("3.1"):
            errors.append("openapi.yaml: must declare openapi: 3.1.0 (found %r)" % ver)
        for must in ("info", "paths", "components"):
            if must not in doc:
                errors.append(f"openapi.yaml: missing top-level '{must}'")
    else:
        errors.append("missing root spec openapi.yaml")

    # -- cross-file $ref resolution -----------------------------------------
    def _unescape(tok: str) -> str:
        return tok.replace("~1", "/").replace("~0", "~")

    resolved = 0
    for f, doc in parsed.items():
        stack = [(doc, f)]
        while stack:
            node, frm = stack.pop()
            if isinstance(node, dict):
                ref = node.get("$ref")
                if isinstance(ref, str):
                    path, _, anchor = ref.partition("#")
                    target_doc = parsed[frm]
                    if path:
                        t = os.path.normpath(os.path.join(os.path.dirname(frm), path))
                        if t not in parsed:
                            errors.append(f"{os.path.relpath(frm, root)}: missing file for $ref {ref}")
                        else:
                            target_doc = parsed[t]
                    anchor = anchor.lstrip("/")
                    if anchor:
                        hop = target_doc
                        for tok in anchor.split("/"):
                            tok = _unescape(tok)
                            if isinstance(hop, dict) and tok in hop:
                                hop = hop[tok]
                            else:
                                errors.append(f"{os.path.relpath(frm, root)}: dangling $ref {ref} (token '{tok}')")
                                break
                        else:
                            resolved += 1
                    else:
                        resolved += 1
                for k, v in node.items():
                    if k != "$ref":
                        stack.append((v, frm))
            elif isinstance(node, list):
                stack.extend((it, f) for it in node)

    if errors:
        for e in errors:
            print("  ✗", e)
        print(f"refs resolved: {resolved}")
        print("FAIL")
        return 1

    print(f"ok: {len(files)} yaml files, {resolved} $refs resolve, root spec valid")
    print("PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())