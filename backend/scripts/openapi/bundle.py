#!/usr/bin/env python3
"""Bundle Pharmaciano's split OpenAPI contract into one self-contained spec.

The split tree (api/openapi) is the single source of truth. Every `$ref` in the
tree points to a FILE (few are file-local '#/Name' refs); bundling inlines
external-file refs AND file-local refs, producing a single, self-contained
OpenAPI document with ZERO residual `$ref`.

Directory conventions:
  - root  openapi.yaml        paths -> { $ref: 'paths/X.yaml#/<pointer>' } and
                              components -> { $ref: 'components/...yaml#/<name>' }
  - paths/*.yaml              full path string (e.g. /auth/login) as a top-level
                              key; operations are resolved inline
  - components/**/*.yaml      component name as a top-level key

Pointer fragments are component/path names; `~1` JSON-pointer escapes are decoded
once ('~1'->'/', '~0'->'~') because path keys literally contain slashes.

Usage:
  python3 scripts/openapi/bundle.py                                    # -> build/openapi.bundled.yaml
  python3 scripts/openapi/bundle.py --out api/scalar/openapi.yaml --server http://localhost:8080,Docker
"""
import argparse
import os
import sys

try:
    import yaml
except ImportError:
    sys.exit("PyYAML required (pip install pyyaml)")

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SPEC_ROOT = os.path.join(ROOT, "api", "openapi")
DEFAULT_OUT = os.path.join(ROOT, "build", "openapi.bundled.yaml")


def is_ref(node):
    return isinstance(node, dict) and set(node.keys()) == {"$ref"}


def ref_split(ref):
    if "#" in ref:
        file_part, _, frag = ref.partition("#")
        return file_part, frag
    return ref, ""


def decode_pointer(fragment):
    return fragment.lstrip("/").replace("~1", "/").replace("~0", "~")


def load_doc(base_dir, file_part):
    path = os.path.normpath(os.path.join(base_dir, file_part))
    with open(path, "r", encoding="utf-8") as f:
        return yaml.safe_load(f), os.path.dirname(path)


def resolve(node, base_dir, document):
    """Return `node` with every $ref inlined (external-file AND file-local).

    base_dir = directory of the source file the content came from;
    document  = that source file's root mapping. External refs load the target
    file and recurse within it; file-local refs ('#/Name') resolve against
    `document`. Result: a flat, self-contained spec with zero $ref remaining.
    """
    if is_ref(node):
        ref = node["$ref"]
        file_part, frag = ref_split(ref)
        key = decode_pointer(frag)
        if file_part == "":
            if key not in document:
                raise ValueError("internal ref %r -> key %r not found in source" % (ref, key))
            return resolve(document[key], base_dir, document)
        target_doc, new_base = load_doc(base_dir, file_part)
        if key not in target_doc:
            raise ValueError("ref %r -> key %r not found in %s"
                             % (ref, key, os.path.normpath(os.path.join(base_dir, file_part))))
        return resolve(target_doc[key], new_base, target_doc)
    if isinstance(node, dict):
        return {k: resolve(v, base_dir, document) for k, v in node.items()}
    if isinstance(node, list):
        return [resolve(v, base_dir, document) for v in node]
    return node


def count_refs(o):
    n = 0
    if isinstance(o, dict):
        if "$ref" in o:
            n += 1
        for v in o.values():
            n += count_refs(v)
    elif isinstance(o, list):
        for v in o:
            n += count_refs(v)
    return n


def main():
    ap = argparse.ArgumentParser(description="Bundle the split OpenAPI contract")
    ap.add_argument("--out", default=DEFAULT_OUT, help="output bundled YAML path")
    ap.add_argument("--server", action="append", default=None,
                    help="override servers (repeatable): url,description")
    args = ap.parse_args()

    doc, base = load_doc(SPEC_ROOT, "openapi.yaml")
    bundled = resolve(doc, base, doc)

    if args.server:
        bundled["servers"] = []
        for s in args.server:
            url, _, desc = s.partition(",")
            bundled["servers"].append({"url": url, "description": desc or "API base"})

    out_dir = os.path.dirname(os.path.abspath(args.out))
    os.makedirs(out_dir, exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as f:
        yaml.safe_dump(bundled, f, sort_keys=False, width=200, allow_unicode=True)

    print("wrote %s" % os.path.abspath(args.out))
    print("  paths: %d   residual $refs: %d"
          % (len(bundled.get("paths", {})), count_refs(bundled)))


if __name__ == "__main__":
    main()