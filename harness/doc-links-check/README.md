# doc-links-check

Containerized validator for local links in the documentation surface and module paths in the
`ARCHITECTURE.md` Core Modules table. It uses only the Python standard library; Python remains inside the image.

## Run

```bash
make check-doc-links
make check-docs
```

The equivalent raw commands are:

```bash
docker build -t doc-links-check harness/doc-links-check
docker run --rm -v "$PWD:/work:ro" doc-links-check
```

The repository is mounted read-only at `/work`. Run the commands from the repository root so relative paths and
reported locations match the host tree.

## What it checks

- Local relative Markdown links in `AGENTS.md`, `ARCHITECTURE.md`, `README.md`, and Markdown files under `docs/`.
- Paths written in the first column of the `ARCHITECTURE.md` Core Modules table.
- Attempts to escape the repository root through a relative path.

External links, in-page anchors, links inside fenced examples, and symlinked Markdown files are intentionally skipped.

