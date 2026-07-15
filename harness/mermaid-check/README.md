# mermaid-check

Offline validator for ` ```mermaid ` blocks in `docs/` and `ARCHITECTURE.md`. Parses each block with
mermaid's own grammar (via `jsdom`, no browser, no network) and fails on parse
errors and renderer-portability hazards.

Runs as a Docker CLI: the npm deps live in the image, so the repo carries no
`node_modules` and no lockfile to maintain — only `Dockerfile` + `check_mermaid.mjs`.

## Run

```
make check-mermaid          # from repo root; also runs inside `make check-docs`
```

The Makefile builds the image (cached after the first run) and mounts the
repository root into it. Equivalent raw commands:

```
docker build -t mermaid-check harness/mermaid-check
docker run --rm -v "$PWD:/work:ro" mermaid-check docs ARCHITECTURE.md   # scans docs/ + ARCHITECTURE.md
docker run --rm -v "$PWD:/work:ro" mermaid-check docs/design-docs
```

The repository is mounted read-only at `/work`, so reported paths read as
`docs/...` or `ARCHITECTURE.md`, matching the host tree.

## What it checks

- **Parse errors** — the block does not compile; GitHub renders a blank error box.
- **Hazards** — parses but renders wrong across mermaid versions (currently:
  literal `\n` line breaks; use `<br/>`).

Authoring rules the diagrams should follow live in
[`docs/design-docs/README.md`](../../docs/design-docs/README.md) (§ Mermaid Rules).

## Version pin

`mermaid` and `jsdom` are pinned in the `Dockerfile`. Keep `mermaid` tracking the
version GitHub renders with; bump that one line when GitHub upgrades so local
validation matches what readers see. That is the only version bookkeeping here.
