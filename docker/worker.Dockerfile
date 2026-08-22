# Default Python Google ADK worker: workflow, model/tool Activities, and DB.
# Same code as the app, different entrypoint. Build context is the repo root.
FROM ghcr.io/astral-sh/uv:python3.13-bookworm-slim

WORKDIR /app/python

COPY python/pyproject.toml python/uv.lock ./
RUN uv sync --frozen --no-install-project --no-dev

COPY python/ ./

CMD ["uv", "run", "--no-sync", "python", "worker.py"]
