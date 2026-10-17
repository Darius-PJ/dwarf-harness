"use strict";
// Fixed graph-helper/v1. Only this absolute, host-verified compiler is loaded.
const fs = require("node:fs");
const crypto = require("node:crypto");
const path = require("node:path").posix;
if (process.version !== process.argv[2]) process.exit(3);
const ts = require(process.argv[3]);
if (ts.version !== process.argv[4]) process.exit(3);
const MAX_FRAME = 8 * 1024 * 1024;
const canonical = value => JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, c => "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0"));
const hash = value => crypto.createHash("sha256").update(canonical(value)).digest("hex");
const compare = (a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b));

function run(req, inputBytes) {
  const start = process.hrtime.bigint();
  const root = "/__graph__";
  const config = req.configuration_sha256;
  const adapter = req.adapter;
  const workspace = req.source.workspace_id;
  let work = 1 + Math.ceil((inputBytes + 4) / 256) + req.corpus.length;
  let nodes = new Map(), edges = new Map(), coverage = [];
  const charge = (n = 1) => { work += n; if (work > req.allowance) throw new RangeError("work"); };
  const unknown = reason => [{scope: "workspace:" + workspace + ":canonical", adapter, state: "unknown", reasons: [reason]}];
  const files = new Map();
  const dirs = new Set([root]);
  const options = {module: ts.ModuleKind.NodeNext, moduleResolution: ts.ModuleResolutionKind.NodeNext, allowJs: true, noEmit: true};
  const sourcePath = name => name.startsWith(root + "/") && !name.includes("\\") && path.normalize(name) === name;
  const host = {
    useCaseSensitiveFileNames: true,
    getCurrentDirectory: () => root,
    fileExists: name => { charge(); return sourcePath(name) && files.has(name); },
    readFile: name => { charge(); return sourcePath(name) ? files.get(name)?.text : undefined; },
    directoryExists: name => { charge(); return dirs.has(name); },
    getDirectories: name => [...dirs].filter(d => { charge(); return path.dirname(d) === name; }).map(d => path.basename(d)),
    realpath: name => { charge(); return name; },
    readDirectory: (name, extensions) => [...files.keys()].filter(f => { charge(); return f.startsWith(name + "/") && (!extensions || extensions.some(e => f.endsWith(e))); }),
    onUnRecoverableConfigFileDiagnostic: () => { throw new SyntaxError("configuration"); },
  };
  const configs = new Map();
  const loading = new Set();
  function loadConfig(name) {
    charge();
    if (configs.has(name)) return configs.get(name);
    if (loading.has(name) || !sourcePath(name) || !files.has(name)) throw new SyntaxError("configuration chain");
    loading.add(name);
    const read = ts.readConfigFile(name, host.readFile);
    if (read.error) throw new SyntaxError("configuration JSON");
    const parsed = ts.parseJsonConfigFileContent(read.config, host, path.dirname(name), options, name);
    // TS18003 is only an empty include set; all corpus sources are still parsed.
    if (parsed.errors.some(d => d.code !== 18003)) throw new SyntaxError("configuration resolution");
    for (const ref of parsed.projectReferences || []) {
      const child = ref.path.endsWith(".json") ? ref.path : path.join(ref.path, "tsconfig.json");
      loadConfig(child);
    }
    const selected = {...parsed.options, ...options};
    configs.set(name, selected); loading.delete(name); return selected;
  }
  try {
    let total = 0;
    for (const item of req.corpus) {
      const name = path.join(root, item.path);
      if (!sourcePath(name) || item.path.split("/").some(p => !p || p === "." || p === "..")) throw new SyntaxError("path");
      const bytes = Buffer.from(item.bytes_base64, "base64"); total += bytes.length;
      if (bytes.length > req.limits.blob_bytes || total > req.limits.corpus_bytes) throw new RangeError("corpus");
      if (crypto.createHash("sha256").update(bytes).digest("hex") !== item.sha256) throw new SyntaxError("digest");
      if (!/\.(ts|tsx|js|jsx|mts|cts|mjs|cjs|json)$/.test(name)) continue;
      const text = new TextDecoder("utf-8", {fatal: true}).decode(bytes);
      files.set(name, {item, text});
      for (let dir = path.dirname(name); dir.startsWith(root); dir = path.dirname(dir)) { dirs.add(dir); if (dir === root) break; }
    }
    for (const name of files.keys()) {
      if (path.basename(name) === "tsconfig.json" || path.basename(name) === "jsconfig.json") loadConfig(name);
      if (/^(tsconfig|jsconfig)\.(js|cjs|mjs|ts)$/.test(path.basename(name))) throw new SyntaxError("executable configuration");
    }
    for (const [filename, file] of files) {
      if (!/\.(ts|tsx|js|jsx|mts|cts|mjs|cjs)$/.test(filename)) continue;
      charge();
      const item = file.item;
      let reason = "";
      let selected = options;
      for (let dir = path.dirname(filename); dir.startsWith(root); dir = path.dirname(dir)) {
        const found = configs.get(path.join(dir, "tsconfig.json")) || configs.get(path.join(dir, "jsconfig.json"));
        if (found) { selected = found; break; }
        if (dir === root) break;
      }
      const source = ts.createSourceFile(filename, file.text, ts.ScriptTarget.Latest, true);
      if (source.parseDiagnostics.length) reason = "unsupported_syntax";
      const references = [];
      function visit(node) {
        charge();
        if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
          if (node.moduleSpecifier) {
            if (ts.isStringLiteral(node.moduleSpecifier)) references.push(node.moduleSpecifier.text);
            else reason = "unsupported_syntax";
          }
        }
        if (ts.isImportEqualsDeclaration(node) || (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword || (ts.isIdentifier(node.expression) && node.expression.text === "require")))) reason = "unsupported_syntax";
        ts.forEachChild(node, visit);
      }
      visit(source);
      const localNodes = new Map(), localEdges = new Map();
      for (const reference of references) {
        charge();
        if (!reference || Buffer.byteLength(reference) > 4096) { reason = "unsupported_syntax"; continue; }
        const resolution = ts.resolveModuleName(reference, filename, selected, host).resolvedModule;
        if (!resolution || !sourcePath(resolution.resolvedFileName) || !files.has(resolution.resolvedFileName)) { if (!reason) reason = "unresolved_reference"; continue; }
        const target = files.get(resolution.resolvedFileName).item;
        const moduleName = resolution.resolvedFileName.slice(root.length + 1);
        const key = {namespace: "workspace", origin_id: workspace, kind: "module", local_key: canonical(["typescript", "workspace", moduleName])};
        if (Buffer.byteLength(key.local_key) > 4096) { reason = "unsupported_syntax"; continue; }
        const node = {key, version_sha256: hash({module_name: moduleName, members: [[target.path, target.sha256]]}), adapter, configuration_sha256: config};
        const evidence = {artifact: item.artifact_key, artifact_sha256: item.sha256, adapter, configuration_sha256: config, reference};
        const edge = {edge_id: hash([key, item.artifact_key, "static_dependency"]), from: key, to: item.artifact_key, type: "static_dependency", geometry: "coupling", weight_ppm: 1000000, evidence};
        localNodes.set(canonical(key), node);
        const old = localEdges.get(edge.edge_id);
        if (!old || compare(canonical(evidence), canonical(old.evidence)) < 0) localEdges.set(edge.edge_id, edge);
      }
      if (!reason) { for (const [k, v] of localNodes) nodes.set(k, v); for (const [k, v] of localEdges) edges.set(k, v); }
      coverage.push({scope: canonical(item.artifact_key), adapter, state: reason ? "unknown" : "complete", reasons: reason ? [reason] : []});
    }
    charge(nodes.size + edges.size + coverage.length);
  } catch (error) {
    nodes = new Map(); edges = new Map(); coverage = unknown(error instanceof RangeError ? "helper_limit" : "unsupported_syntax");
    if (error instanceof RangeError) work = req.allowance;
  }
  const out = {protocol: req.protocol, analysis_key: req.analysis_key, adapter, configuration_sha256: config, next_cursor: {artifact_index: req.corpus.length, output_index: 0}, work_used: work, nodes: [...nodes.values()].sort((a, b) => compare(canonical(a.key), canonical(b.key))), edges: [...edges.values()].sort((a, b) => compare(a.edge_id, b.edge_id)), coverage: coverage.sort((a, b) => compare(a.scope, b.scope) || compare(a.adapter, b.adapter)), cost: {wall_ns: Number(process.hrtime.bigint() - start), input_bytes: inputBytes, output_bytes: 0}};
  for (;;) {
    const required = work + Math.ceil((Buffer.byteLength(canonical(out)) + 4) / 256);
    if (required > req.allowance) { out.nodes = []; out.edges = []; out.coverage = unknown("helper_limit"); out.work_used = req.allowance; break; }
    if (required === out.work_used) break;
    out.work_used = required;
  }
  return out;
}

function readExact(buffer) {
  let offset = 0;
  while (offset < buffer.length) {
    const n = fs.readSync(0, buffer, offset, buffer.length - offset, null);
    if (!n) process.exit(2);
    offset += n;
  }
}
function writeAll(buffer) {
  let offset = 0;
  while (offset < buffer.length) {
    const n = fs.writeSync(1, buffer, offset, buffer.length - offset);
    if (!n) process.exit(2);
    offset += n;
  }
}
const header = Buffer.alloc(4);
readExact(header);
const size = header.readUInt32BE(0);
if (size > MAX_FRAME - 4) process.exit(2);
const input = Buffer.alloc(size);
readExact(input);
if (fs.readSync(0, Buffer.alloc(1), 0, 1, null)) process.exit(2);
const response = Buffer.from(canonical(run(JSON.parse(input.toString("utf8")), size)));
if (response.length > MAX_FRAME - 4) process.exit(2);
header.writeUInt32BE(response.length, 0);
writeAll(header); writeAll(response);
