# Demo run: Google ADK agents made durable by Temporal

## Setup

Run `make up`, then open the travel UI and Temporal UI. Keep
[`python/workflows/agent.py`](python/workflows/agent.py) or
[`go/internal/travelagent/workflows.go`](go/internal/travelagent/workflows.go)
ready in the editor.

The framing: “This is the exact travel experience from the canonical AI demo.
The difference is inside the worker: Google ADK owns reasoning and tool choice;
Temporal owns durable execution.”

## 1. Show the integration boundary

Point out three lines in the active language:

- `TemporalModel` / `googleadk.NewModel`: every Gemini turn is a Temporal
  Activity with timeout and retry policy.
- `activity_tool` / `googleadk.ActivityAsTool`: normal typed Activities become
  ADK tools without a second dispatch layer.
- `GoogleAdkPlugin` / `googleadk.NewPlugin`: worker-side model reconstruction,
  payload conversion, and deterministic ADK runtime support.

“ADK still feels like ADK, and Temporal still feels like Temporal.”

## 2. Tool loop and durable state

Ask: “Plan a trip to Tokyo from SFO.”

ADK chooses catalog tools and stages stable IDs in the itinerary. Open history
to show the model and tool Activities. The itinerary itself lives in workflow
state, so refresh the browser or restart the worker without losing it.

## 3. Human-in-the-loop

Ask: “Book it.” The ADK tool requests confirmation. The existing frontend shows
the same approval card; the workflow is waiting durably rather than holding an
HTTP request or process-local future.

Approve. ADK resumes with its confirmation response and calls the child
`CheckoutWorkflow`.

## 4. Compensation

With the default `CHECKOUT_FAIL_HOTEL=true`, checkout reserves the flight, the
hotel supplier fails, and the child compensates with `cancel_flight`. Point to
the ordered Activities in history.

“ADK decides what should happen. The child workflow guarantees how it happens.”

## 5. Parallel research and retry

Ask: “Do a deep research pass for five days in Tokyo for a first-time visitor
who loves food.” Watch the progress panel advance through planning, parallel
Google Search Activities, and writing.

`WEB_SEARCH_FAIL_RATE=0.4` injects some first-attempt failures. Temporal retries
only failed branches; completed findings replay from history.

## 6. Crash recovery

During research:

```bash
make kill-worker
make worker
```

The UI pauses and resumes. No application recovery code reconstructs the ADK
session: workflow replay rebuilds it, and existing Activity results come from
history.

## 7. Switch languages

Stop Python, start Go, then create a new conversation:

```bash
make kill-worker
cd go && make worker
```

The gateway and frontend do not change because both implementations honor
[`CONTRACT.md`](CONTRACT.md). The histories are language-specific, so switching
workers is demonstrated with a fresh conversation.

## Close

“A framework gives the agent reasoning and tools. Temporal gives that framework
durability: retries, recovery, observable state, long-lived approvals, parallel
work, and compensation.”
