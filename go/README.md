# Go worker

The Go implementation uses the maintained
`go.temporal.io/sdk/contrib/googleadk` integration and Google ADK v2.

The important pieces are:

- [`internal/travelagent/workflows.go`](internal/travelagent/workflows.go): ADK
  runner, durable session, confirmation bridge, research fan-out, and checkout.
- [`internal/travelagent/adk_tools.go`](internal/travelagent/adk_tools.go): typed
  Activity inputs, catalog Activities, and Gemini research Activities.
- [`cmd/worker/main.go`](cmd/worker/main.go): installs `googleadk.NewPlugin`, the
  Gemini model factory, workflows, and Activities.

`googleadk.NewModel` turns ADK model calls into Temporal Activities.
`googleadk.ActivityAsTool` infers Gemini schemas from Go structs and dispatches
the actual tool execution as a Temporal Activity. ADK's confirmation event is
translated to the same gateway-facing approval object used by Python.

## Run

Configure the root `.env`, stop any Python worker, then:

```bash
cd go
make up
```

The shared UI is <http://localhost:5173>; Temporal UI is
<http://localhost:8233>.

## Test and image

```bash
go test ./...
docker build -f ../docker/worker-go.Dockerfile ..
```

Configuration uses the same `GOOGLE_API_KEY`, `ADK_MODEL`, Temporal, database,
research, and checkout variables documented by the root `.env.example`.
