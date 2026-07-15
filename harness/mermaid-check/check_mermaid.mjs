// Offline validator for ```mermaid blocks in Markdown docs.
//
// Parses every fenced mermaid block with mermaid's own grammar (no browser,
// no network) and flags renderer-portability hazards that parse-OK but draw
// wrong across mermaid versions. Exits non-zero on any failure so it can gate
// `make check-docs`.
//
// Usage: node check_mermaid.mjs [rootDir ...]   (default: docs)

import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";

// mermaid needs a DOM even to parse (DOMPurify, config). Set up jsdom globals
// before importing mermaid.
const dom = new JSDOM("<!DOCTYPE html><body></body>", { pretendToBeVisual: true });
global.window = dom.window;
global.document = dom.window.document;
global.navigator = dom.window.navigator;

const mermaid = (await import("mermaid")).default;
mermaid.initialize({ startOnLoad: false, securityLevel: "loose" });

const roots = process.argv.slice(2);
if (roots.length === 0) roots.push("docs");

function walk(dir, out) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name.startsWith(".")) continue;
      walk(full, out);
    } else if (entry.name.endsWith(".md")) {
      out.push(full);
    }
  }
}

// Extract fenced ```mermaid blocks. `fence` is the 1-based line of the opening
// fence, which is what an editor jumps to.
function extractBlocks(text) {
  const blocks = [];
  const lines = text.split("\n");
  let inBlock = false, buf = [], fence = 0;
  for (let i = 0; i < lines.length; i++) {
    if (!inBlock && /^\s*```mermaid\s*$/.test(lines[i])) {
      inBlock = true; buf = []; fence = i + 1; continue;
    }
    if (inBlock && /^\s*```\s*$/.test(lines[i])) {
      blocks.push({ fence, code: buf.join("\n") }); inBlock = false; continue;
    }
    if (inBlock) buf.push(lines[i]);
  }
  return blocks;
}

// Portability hazards that mermaid.parse() accepts but that render wrong or
// version-dependently. Kept deliberately narrow to avoid false positives.
function hazards(code) {
  const found = [];
  if (/\\n/.test(code)) {
    found.push("literal '\\n' line break — use <br/> (renderer-dependent; the repo standard is <br/>)");
  }
  return found;
}

const files = [];
for (const root of roots) {
  if (!fs.existsSync(root)) { console.error(`path not found: ${root}`); process.exit(2); }
  const st = fs.statSync(root);
  if (st.isDirectory()) walk(root, files);
  else if (root.endsWith(".md")) files.push(root);
}

let blockCount = 0, failCount = 0;
for (const file of files.sort()) {
  const text = fs.readFileSync(file, "utf8");
  for (const b of extractBlocks(text)) {
    blockCount++;
    const loc = `${file}:${b.fence}`;
    try {
      await mermaid.parse(b.code);
    } catch (e) {
      failCount++;
      const msg = (e && e.message ? e.message : String(e)).split("\n").slice(0, 4).join(" | ");
      console.log(`FAIL   ${loc}  parse error: ${msg}`);
      continue;
    }
    const hz = hazards(b.code);
    if (hz.length) {
      failCount++;
      console.log(`HAZARD ${loc}  ${hz.join("; ")}`);
    } else {
      console.log(`ok     ${loc}`);
    }
  }
}

console.log(`\n${blockCount} mermaid block(s), ${failCount} problem(s).`);
process.exit(failCount > 0 ? 1 : 0);
