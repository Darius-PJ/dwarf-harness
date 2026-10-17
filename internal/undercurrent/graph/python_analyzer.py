"""Fixed graph-helper/v1: parse source and find specs, never import targets."""
import ast
import base64
import hashlib
import importlib.machinery
import json
import os
import pathlib
import struct
import sys
import time

MAX_FRAME = 8 * 1024 * 1024


def canonical(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).replace("&", "\\u0026").replace("<", "\\u003c").replace(">", "\\u003e").replace("\u2028", "\\u2028").replace("\u2029", "\\u2029")


def sha(value):
    return hashlib.sha256(canonical(value).encode("utf-8")).hexdigest()


def run(req):
    start = time.monotonic_ns()
    root = pathlib.Path(req["materialization_root"])
    config = req["configuration_sha256"]
    adapter = req["adapter"]
    workspace = req["source"]["workspace_id"]
    allowance = req["allowance"]
    work = 1 + (len(canonical(req).encode("utf-8")) + 4 + 255) // 256 + len(req["corpus"])
    nodes, edges, coverage = {}, {}, []
    files = {}
    hook_cache = {}

    def charge(n=1):
        nonlocal work
        work += n
        if work > allowance:
            raise OverflowError()

    def unknown(reason):
        return [{"scope": "workspace:" + workspace + ":canonical", "adapter": adapter, "state": "unknown", "reasons": [reason]}]

    def materialize():
        seen = set()
        directory_identities = {}
        total = 0
        for item in req["corpus"]:
            name = item["path"]
            parts = name.split("/")
            if any(p in ("", ".", "..") for p in parts) or "\\" in name or "\x00" in name:
                raise ValueError()
            target = root.joinpath(*parts)
            # Refuse host aliases (including Windows case/device/ADS/trailing-dot
            # rules), not normalize two Git identities into one native file.
            for part in parts:
                if os.name == "nt" and (":" in part or part.endswith((".", " ")) or part.split(".")[0].upper() in {"CON", "PRN", "AUX", "NUL", *["COM" + str(n) for n in range(10)], *["LPT" + str(n) for n in range(10)]}):
                    raise ValueError()
            normalized = os.path.normcase(str(target))
            if normalized in seen:
                raise ValueError()
            seen.add(normalized)
            data = base64.b64decode(item["bytes_base64"], validate=True)
            total += len(data)
            if len(data) > req["limits"]["blob_bytes"] or total > req["limits"]["corpus_bytes"]:
                raise OverflowError()
            if hashlib.sha256(data).hexdigest() != item["sha256"]:
                raise ValueError()
            for count in range(1, len(parts)):
                charge()
                directory = root.joinpath(*parts[:count])
                directory.mkdir(exist_ok=True)
                stat = directory.stat()
                identity = (stat.st_dev, stat.st_ino)
                spelling = "/".join(parts[:count])
                if identity in directory_identities and directory_identities[identity] != spelling:
                    raise ValueError()
                directory_identities[identity] = spelling
            with target.open("xb") as stream:
                stream.write(data)
            files[str(target)] = item

    def has_import_hooks(item):
        name = item["path"]
        if name in hook_cache:
            return hook_cache[name]
        try:
            tree = ast.parse(base64.b64decode(item["bytes_base64"]), filename=name)
        except (SyntaxError, ValueError, UnicodeError):
            hook_cache[name] = True
            return True
        system_names, hook_names = {"sys"}, set()
        walked = []
        for node in ast.walk(tree):
            charge()
            walked.append(node)
            if isinstance(node, ast.Import):
                system_names.update(alias.asname or "sys" for alias in node.names if alias.name == "sys")
            if isinstance(node, ast.ImportFrom) and node.module == "sys":
                hook_names.update(alias.asname or alias.name for alias in node.names if alias.name in ("path", "meta_path", "path_hooks"))
        unsafe = any(
            isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name) and node.value.id in system_names and node.attr in ("path", "meta_path", "path_hooks")
            or isinstance(node, ast.Name) and (node.id in hook_names or node.id == "__path__" and isinstance(node.ctx, ast.Store))
            for node in walked
        )
        hook_cache[name] = unsafe
        return unsafe

    def resolve(name):
        locations = [str(root)]
        spec = None
        for part in name.split("."):
            charge()
            # Resolve each segment against only its native parent's locations;
            # no dotted-name lookup may demand a parent in sys.modules.
            spec = importlib.machinery.PathFinder.find_spec(part, locations)
            if spec is None:
                return None
            locations = list(spec.submodule_search_locations or [])
            if spec.origin is not None and spec.origin not in files:
                return None
            if spec.origin is not None and files[spec.origin]["path"].endswith("/__init__.py") and has_import_hooks(files[spec.origin]):
                return None
        if spec is None:
            return None
        members = []
        if spec.origin is not None:
            item = files.get(spec.origin)
            if item is None or not item["path"].endswith(".py"):
                return None
            members = [[item["path"], item["sha256"]]]
        else:
            # PathFinder-confirmed namespace locations: membership is the
            # supplied source under those exact locations, never external files.
            for location in locations:
                prefix = pathlib.Path(location)
                for filename, item in files.items():
                    charge()
                    if pathlib.Path(filename).is_relative_to(prefix) and item["path"].endswith(".py"):
                        members.append([item["path"], item["sha256"]])
            if not members:
                return None
        members.sort(key=lambda x: x[0].encode("utf-8"))
        key = {"namespace": "workspace", "origin_id": workspace, "kind": "module", "local_key": canonical(["python", "workspace", name])}
        if len(key["local_key"].encode("utf-8")) > 4096:
            return None
        node = {"key": key, "version_sha256": sha({"module_name": name, "members": members}), "adapter": adapter, "configuration_sha256": config}
        return node

    try:
        materialize()
        for item in req["corpus"]:
            if not item["path"].endswith(".py"):
                continue
            charge()
            reason = ""
            references = []
            try:
                tree = ast.parse(base64.b64decode(item["bytes_base64"]), filename=item["path"])
                if has_import_hooks(item):
                    reason = "unsupported_syntax"
                top = set(tree.body)
                for node in ast.walk(tree):
                    charge()
                    if isinstance(node, ast.Call) and ((isinstance(node.func, ast.Name) and node.func.id == "__import__") or (isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name) and node.func.value.id == "importlib")):
                        reason = "unsupported_syntax"
                    if isinstance(node, (ast.Assign, ast.AugAssign, ast.AnnAssign)):
                        targets = getattr(node, "targets", [getattr(node, "target", None)])
                        if any(isinstance(t, ast.Attribute) and isinstance(t.value, ast.Name) and t.value.id == "sys" and t.attr in ("path", "meta_path", "path_hooks") for t in targets):
                            reason = "unsupported_syntax"
                    if isinstance(node, (ast.Import, ast.ImportFrom)):
                        if node not in top:
                            reason = "unsupported_syntax"
                            continue
                        if isinstance(node, ast.Import):
                            references.extend(alias.name for alias in node.names)
                        else:
                            base = node.module or ""
                            if node.level:
                                package = item["path"].split("/")[:-1]
                                if node.level > len(package):
                                    reason = "unresolved_reference"
                                    continue
                                base = ".".join(package[:len(package) - node.level + 1] + ([base] if base else []))
                            if not base:
                                reason = "unresolved_reference"
                                continue
                            for alias in node.names:
                                candidate = base + "." + alias.name
                                parent = resolve(base)
                                child = resolve(candidate) if alias.name != "*" else None
                                if child is not None:
                                    references.append(candidate)
                                elif parent is not None:
                                    references.append(base)
                                else:
                                    reason = "unresolved_reference"
            except (SyntaxError, ValueError, UnicodeError):
                reason = "unsupported_syntax"
            local_nodes, local_edges = {}, {}
            for reference in references:
                charge()
                resolved = resolve(reference)
                if resolved is None or len(reference.encode("utf-8")) > 4096:
                    if not reason:
                        reason = "unresolved_reference"
                    continue
                key = resolved["key"]
                evidence = {"artifact": item["artifact_key"], "artifact_sha256": item["sha256"], "adapter": adapter, "configuration_sha256": config, "reference": reference}
                edge = {"edge_id": sha([key, item["artifact_key"], "static_dependency"]), "from": key, "to": item["artifact_key"], "type": "static_dependency", "geometry": "coupling", "weight_ppm": 1000000, "evidence": evidence}
                local_nodes[canonical(key)] = resolved
                old = local_edges.get(edge["edge_id"])
                if old is None or canonical(evidence).encode("utf-8") < canonical(old["evidence"]).encode("utf-8"):
                    local_edges[edge["edge_id"]] = edge
            if not reason:
                nodes.update(local_nodes)
                edges.update(local_edges)
            coverage.append({"scope": canonical(item["artifact_key"]), "adapter": adapter, "state": "unknown" if reason else "complete", "reasons": [reason] if reason else []})
        charge(len(nodes) + len(edges) + len(coverage))
    except OverflowError:
        nodes, edges, coverage = {}, {}, unknown("helper_limit")
        work = allowance
    except (ValueError, OSError, ImportError, AttributeError):
        nodes, edges, coverage = {}, {}, unknown("unsupported_syntax")
    out = {"protocol": req["protocol"], "analysis_key": req["analysis_key"], "adapter": adapter, "configuration_sha256": config, "next_cursor": {"artifact_index": len(req["corpus"]), "output_index": 0}, "work_used": work, "nodes": sorted(nodes.values(), key=lambda n: canonical(n["key"]).encode("utf-8")), "edges": sorted(edges.values(), key=lambda e: e["edge_id"]), "coverage": sorted(coverage, key=lambda c: (c["scope"].encode("utf-8"), c["adapter"])), "cost": {"wall_ns": time.monotonic_ns() - start, "input_bytes": 0, "output_bytes": 0}}
    while True:
        required = work + (len(canonical(out).encode("utf-8")) + 4 + 255) // 256
        if required > allowance:
            out["nodes"], out["edges"], out["coverage"], out["work_used"] = [], [], unknown("helper_limit"), allowance
            break
        if required == out["work_used"]:
            break
        out["work_used"] = required
    return out


if __name__ == "__main__":
    if sys.version.split()[0] != sys.argv[1]:
        raise SystemExit(3)
    header = sys.stdin.buffer.read(4)
    if len(header) != 4:
        raise SystemExit(2)
    size = struct.unpack(">I", header)[0]
    if size > MAX_FRAME - 4:
        raise SystemExit(2)
    data = sys.stdin.buffer.read(size)
    if len(data) != size or sys.stdin.buffer.read(1):
        raise SystemExit(2)
    response = canonical(run(json.loads(data))).encode("utf-8")
    if len(response) > MAX_FRAME - 4:
        raise SystemExit(2)
    sys.stdout.buffer.write(struct.pack(">I", len(response)) + response)
