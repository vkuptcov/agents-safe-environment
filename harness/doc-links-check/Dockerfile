# Documentation link and architecture-path validator. Python stays inside the
# image, so the host needs only Docker to run the complete documentation gate.
FROM python:3.13-alpine

WORKDIR /app
COPY check_doc_links.py /app/check_doc_links.py

# The repository is mounted read-only at run time. The checker resolves every
# candidate and module path relative to this working directory.
WORKDIR /work
ENTRYPOINT ["python3", "/app/check_doc_links.py"]

