# Python worker

The Python implementation uses the maintained Temporal integration shipped in
`temporalio.contrib.google_adk_agents`.

The important pieces are:

- [`workflows/agent.py`](workflows/agent.py): builds the ADK `Agent`, owns the
  durable ADK session, bridges confirmation to Temporal, and retains itinerary
  and research state.
- [`worker.py`](worker.py): installs `GoogleAdkPlugin`, which registers model
  Activities, the ADK payload converter, and deterministic runtime providers.
- [`activities/tools.py`](activities/tools.py): typed catalog Activities exposed
  to ADK with `activity_tool`.
- [`activities/research.py`](activities/research.py): durable Gemini plan/search/
  write Activities.
- [`workflows/checkout.py`](workflows/checkout.py): compensating booking child.

## Run

From the repository root:

```bash
cp .env.example .env
# add GOOGLE_API_KEY
make up
```

Or from this directory, run `make up`. Use `make kill-worker` and `make worker`
for the crash-recovery demonstration.

## Test

```bash
uv sync --frozen
uv run pytest -q
```

## Configuration

| Variable | Default |
|---|---|
| `GOOGLE_API_KEY` (or `GEMINI_API_KEY`) | required |
| `ADK_MODEL` | `gemini-3.6-flash` |
| `TEMPORAL_ADDRESS` | `localhost:7233` |
| `TEMPORAL_NAMESPACE` | `default` |
| `DB_URL` | `postgresql://demo:demo@localhost:5432/travel` |
| `RESEARCH_SEARCHES` | `6` |
| `WEB_SEARCH_FAIL_RATE` | `0.4` |
| `CHECKOUT_FAIL_HOTEL` | `true` |

Temporal Cloud also supports `TEMPORAL_API_KEY`, or the existing mTLS cert/key
variables.
