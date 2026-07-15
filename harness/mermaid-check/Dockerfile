# Offline mermaid validator for docs/. The npm deps live in this image, not in
# the repo — nothing to install locally, nothing to track under version control.
#
# Build:  docker build -t mermaid-check harness/mermaid-check
# Run:    docker run --rm -v "$PWD:/work:ro" mermaid-check docs ARCHITECTURE.md
# (wired into `make check-mermaid`)
FROM node:20-alpine

# Pinned so local validation matches the version GitHub renders with.
# Bump these two lines when GitHub upgrades its mermaid; that is the only
# version bookkeeping this tool needs.
WORKDIR /app
RUN npm install --no-audit --no-fund --loglevel=error mermaid@11.12.0 jsdom@22.1.0

COPY check_mermaid.mjs /app/check_mermaid.mjs

# The repository is mounted here at run time; keep cwd relative so reported
# paths read as `docs/...` or `ARCHITECTURE.md`, matching the host tree.
WORKDIR /work
ENTRYPOINT ["node", "/app/check_mermaid.mjs"]
CMD ["docs"]
