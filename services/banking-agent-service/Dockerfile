FROM python:3.13-slim AS builder
ENV UV_PYTHON_DOWNLOADS=never \
    UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1
WORKDIR /app
RUN python -m pip install --no-cache-dir uv==0.13.0
COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-dev --no-install-project --python /usr/local/bin/python

FROM python:3.13-slim
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONPATH=/app/src \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /app
RUN groupadd --gid 10001 banking-agent \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin banking-agent
COPY --from=builder /app/.venv /app/.venv
COPY src /app/src
COPY migrations /app/migrations
USER 10001:10001
EXPOSE 8091
CMD ["python", "-m", "uvicorn", "banking_agent.main:app", "--host", "0.0.0.0", "--port", "8091", "--no-server-header", "--no-access-log"]
