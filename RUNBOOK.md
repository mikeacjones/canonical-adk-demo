# Runbook: Canonical Google ADK demo

This runbook is for presenting the demo. The [README](README.md) explains what it is and how it's built, and [TALK_TRACK.md](TALK_TRACK.md) has a shorter script.

## What it shows

A traveller plans, researches, and books a trip by chatting with a Google ADK agent. Google ADK decides which model and tool calls to make. Temporal runs each of those calls as an Activity, keeps the conversation in a Workflow, and books the trip in a child Workflow that undoes its own partial work.

The demo makes three problems visible:

- **One agent turn makes many model and tool calls, and any of them can fail.** A web search times out, the database drops, the Worker process dies mid-turn. Beats 1 to 3 show each call retried on its own, and the turn carrying on from where it stopped after a crash.
- **Booking waits on a person, and the person may take a while.** The agent asks for approval and then waits for as long as it takes, through Worker restarts. Beat 4 shows it.
- **A trip books several suppliers, and one of them fails after another has succeeded.** The flight is reserved, the hotel fails, and the flight has to be cancelled. Beat 5 shows the child Workflow doing that.

## Before you start

- Install Docker, the [Temporal CLI](https://docs.temporal.io/cli), and [uv](https://docs.astral.sh/uv/). The Python Worker pins Python 3.13 in `python/.python-version`; uv installs it. Go 1.25 or later is only needed for the Go Worker.
- You need a Gemini API key. Every chat turn calls Gemini, and the research beat also uses Gemini's Google Search grounding, so each run costs a little.
- Clone the repo, then `cp .env.example .env` and set `GOOGLE_API_KEY` in `.env`. The first `make up` installs the Python dependencies.
- The Python Worker is the one tmprl-demo.cloud deploys, and the one this runbook follows. The Go Worker behaves the same; see [If someone asks about Go](#if-someone-asks-about-go).

### On tmprl-demo.cloud

The catalog entry runs the gateway and the Python Worker against Temporal Cloud with `gemini-3.6-flash`. You sign in through the platform's auth gate, and your email becomes the traveller and part of the Workflow ID (`trip-<your-email-slug>-<4 hex>`).

- To present the crash beat, open the demo's page in the catalog and press **Create crashable environment**. It gives you a private copy of the Workers on their own Task Queue (`travel-agent-<workspace>`), and deletes itself after an hour (**Extend +1h** to keep it). Press **Open environment** to get its URL.
- The make targets in the UI's **Demo controls** drawer (`make kill-worker`, `make kill-db`) only work locally. On the platform, use **Crash workers** or **Scale down workers** on the catalog page instead. There's no database outage control on the platform.

### On your own Temporal Cloud Namespace

1. Set `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE`, and `TEMPORAL_API_KEY` (or `TEMPORAL_TLS_CERT` and `TEMPORAL_TLS_KEY`). The Worker reads them from `.env`, but the gateway doesn't read `.env`, so export them in the shell you run `make up` from as well.
2. `make up` still starts a local dev server; nothing connects to it once those variables are set.
3. Count on Queries: while a turn runs, the page sends two Queries every 700 ms (research progress and itinerary), and each research Activity sends one. Queries are billable Actions. A conversation that's waiting for its next message sends nothing.

## Start it

From the repository root:

```bash
make up
```

It starts Postgres in Docker (seeded with the travel catalog), a Temporal dev server, the Python Worker, the gateway on port 8000, and a static web server on port 5173. Then open:

- the travel UI at http://localhost:8000 (the gateway serves it). http://localhost:5173 works too, but its **Demo controls** drawer shows a placeholder model name.
- the Temporal UI at http://localhost:8233

A healthy start shows **Travel Planner Agent**, a greeting from the agent, and an empty **Itinerary** rail on the left with **Book trip** disabled. `make status` lists every component as running, and `/tmp/travel-worker.log` shows `python Google ADK worker polling 'travel-agent' on localhost:7233 (model: gemini-3.6-flash)`.

No Workflow exists until you send the first message. After that, a **workflowId:** link appears under the title; it opens this conversation's Workflow in the Temporal UI. Keep that tab open next to the chat.

## The walkthrough

Model output varies, so the agent's wording and the exact flights and hotels it picks change from run to run. The prompts below come from the README and talk track.

### 1. The agent plans a trip, and every call is an Activity

1. Type `Plan a trip to Tokyo from SFO.` and press `Enter`.
2. The agent searches the catalog and replies with flights, hotels, and things to do. If it doesn't stage anything, ask `Add the cheapest flight and a hotel to my itinerary.` The items appear in the **Itinerary** rail as the agent adds them.
3. Click the **workflowId:** link. In the Event History, each Gemini call is an `invoke_model` Activity with the summary **Google ADK model turn**, and each catalog lookup is its own Activity, such as **Search flights** or **Resolve itinerary items**.
4. Say: "This is a normal ADK agent. ADK chose those tools. The difference is that each model call and each tool call is a Temporal Activity, with its own timeout and retry policy, recorded in the Workflow's history."

### 2. Deep research fans out, and only the failed searches retry

1. Type `Do a deep research pass for five days in Tokyo for a first-time visitor who loves food.`
2. A **Researching…** card replaces the typing bubble. It moves through **Planning searches**, **Researching the web (n/6)**, and **Writing the guide**, and lists the search plan with a spinner per search. The finished guide arrives as the agent's reply.
3. Each search's first attempt fails 40% of the time on purpose (`WEB_SEARCH_FAIL_RATE=0.4`). In the Temporal UI, open a **Search: …** Activity: a retried one shows **Attempt** 2 and a **Last Failure** of `web_search transient failure (simulated)`. While a search is still retrying, the **Pending Activities** view shows its attempt count.
4. Say: "Six searches run in parallel. When one fails, Temporal retries that one search. The others don't run again, and neither does the planning step."

### 3. Crash the Worker mid-turn

1. Send another message, for example `Find me a hotel under $300 a night in Tokyo and add it.`
2. While the typing bubble shows, stop the Worker: run `make kill-worker` locally, or press **Crash workers** on the catalog page.
3. The chat keeps waiting. In the Temporal UI, the Workflow is still **Running** and the in-flight Activity sits in **Pending Activities**.
4. Run `make worker` (on the platform, the Workers come back by themselves). The turn finishes and the reply arrives.
5. Say: "The process died in the middle of an agent loop. The new Worker replays the Workflow's history to rebuild the ADK session, and reuses every model and tool result it already has. Nothing in the code reloads a session from a database."

`make kill-worker` runs `pkill -f "worker.py"`, so it also stops any other process on your machine with `worker.py` in its command line, such as another demo's Worker. Stop other demos first, or kill the Worker's PID yourself.

How long the turn waits depends on what was running when the Worker died. Temporal retries an Activity whose Worker disappeared when that Activity's Start-To-Close Timeout passes: 30 seconds for a catalog tool, 2 minutes for a model turn or a web search. Fill the gap by showing the pending Activity and its timeout.

### 4. The agent asks for approval and waits

1. Press **Book trip** in the Itinerary rail. It sends `I’d like to book this trip.` as a message.
2. An approval card appears: **⏸ Agent requests durable checkout**, the item count and total, and **Confirm** and **Cancel**.
3. Say: "ADK asked for confirmation before calling `book_trip`. The Workflow is waiting for a Signal. No HTTP request or process is holding that wait open."
4. To prove it, run `make kill-worker`, then `make worker`, before you press anything. Start the Worker again before you press **Confirm**: the button first checks the pending approval with a Query, and Queries need a running Worker.
5. Press **Confirm**. In the Temporal UI, the history shows the `confirm_action` Signal.

For an invoice instead of a full booking, ask `Find events in Auckland in March, then flights from SFO`, pick a flight, and ask for an invoice. The card reads **🧾 Agent is ready to run: create_invoice**.

### 5. The hotel fails, and checkout cancels the flight

1. After **Confirm**, the bubble reads **running CheckoutWorkflow…**.
2. The agent replies that the hotel booking failed and the flight was cancelled. The hotel failure is on by default (`CHECKOUT_FAIL_HOTEL=true`).
3. In the Temporal UI, open the child Workflow `<workflow ID>-checkout-1`. Its history shows **Book flight: …** completed, **Book hotel: …** failed after one attempt with `HotelBookingFailed`, then **Cancel flight: …** completed. The child Workflow completes with status `compensated`, and the conversation Workflow keeps running.
4. Say: "ADK decided to book. The child Workflow decides how: book in order, and if a step fails, undo the steps that succeeded, in reverse. If the Worker had crashed between the hotel failure and the cancellation, the cancellation would still run."

### 6. Optional: a provider outage or a database outage

- **Model provider outage.** Open **Demo controls** (top right) and turn off the **LLM API** switch; the dot on the button turns amber. Send a message. The turn waits inside the Workflow before ADK makes any model call. Turn the switch back on and the turn runs. Flip it between turns, not during research: research Activities fail with `LLMProviderDown` while it's off and give up after 4 attempts.
- **Database outage (local only).** Run `make kill-db` while a catalog tool is about to run. The tool Activity retries with backoff, up to 10 seconds apart, with no attempt limit. Run `make db` and the next attempt succeeds.

## Show it in code

All links point at commit [`51f9c8f`](https://github.com/temporal-sa/canonical-adk-demo/commit/51f9c8f34352ff518fdee0a32028eb3c997deb32). Open a file locally with `code -g <path>:<line>`.

### Run every model call and tool call as a Temporal Activity

**When to use it:** after beat 1, or when someone asks what it took to make an ADK agent durable.

**Where it is:** [`python/workflows/agent.py` lines 250–277](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L250-L277) (`code -g python/workflows/agent.py:250`). The plugin is installed in [`python/worker.py` lines 38–45](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/worker.py#L38-L45).

```python
return Agent(
    name="travel_planner",
    model=TemporalModel(
        config.ADK_MODEL,
        activity_config=ActivityConfig(
            start_to_close_timeout=timedelta(minutes=2),
            retry_policy=MODEL_RETRY,
            summary="Google ADK model turn",
        ),
    ),
    description="A friendly travel planner with durable tools and approvals.",
    instruction=system_prompt(traveller_email),
    tools=[
        activity_tool(search_events, summary="Search events", **tool_options),
        # ...
        activity_tool(search_flights, summary="Search flights", **tool_options),
        # ...
        FunctionTool(func=add_to_itinerary),
        FunctionTool(func=remove_from_itinerary),
        FunctionTool(func=research_destination),
        FunctionTool(func=create_invoice, require_confirmation=True),
        FunctionTool(func=book_trip, require_confirmation=True),
    ],
)
```

```python
async def main() -> None:
    client = await config.temporal_client(plugins=[GoogleAdkPlugin()])
    # ...
        worker = Worker(
            client,
            task_queue=config.TASK_QUEUE,
            workflows=[TravelAgentWorkflow, CheckoutWorkflow],
```

**What you need to understand:**

- `TemporalModel` takes the place of a model name. Each model call runs as an `invoke_model` Activity. Only the model's name crosses into the Activity; the Worker builds the real Gemini client there, so `GOOGLE_API_KEY` lives on the Worker and never in Workflow code.
- `activity_tool` wraps an ordinary `@activity.defn` function so ADK sees its typed signature as the tool schema, and calling the tool runs `workflow.execute_activity`. Each catalog lookup gets the tool timeout and retry policy (next entry).
- The `FunctionTool` tools are Workflow code. `add_to_itinerary` changes Workflow state, `research_destination` schedules several Activities, and `book_trip` starts a child Workflow. A tool can do anything a Workflow can.
- `GoogleAdkPlugin` goes on the Client, and the Worker built from that Client picks it up. It registers the model Activities, sets a Pydantic payload converter for ADK's types, lets `google.adk` and `google.genai` through the Workflow sandbox, and makes ADK's clock and IDs deterministic so the Workflow can replay.
- The ADK runner and session live inside the Workflow. On replay, ADK runs again and the model and tool results come from history instead of being called again.
- The integration is pre-release, and the repo pins `temporalio` 1.31.0. The current docs call the tool wrapper `activity_as_tool`.

**Docs:** [Google ADK integration (Python)](https://docs.temporal.io/develop/python/integrations/google-adk)

### Give model calls and tool calls different retry rules

**When to use it:** during beats 2 and 6, or when someone asks what happens when Gemini or the database is down.

**Where it is:** [`python/workflows/agent.py` lines 58–70](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L58-L70) (`code -g python/workflows/agent.py:58`), applied to the tools in [lines 128–131](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L128-L131). A rejected key becomes a non-retryable error in [`python/interceptors.py` lines 25–36](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/interceptors.py#L25-L36).

```python
MODEL_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=1),
    backoff_coefficient=2.0,
    maximum_interval=timedelta(seconds=10),
    maximum_attempts=4,
    non_retryable_error_types=["LLMFatalError"],
)
TOOL_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=1),
    backoff_coefficient=2.0,
    maximum_interval=timedelta(seconds=10),
    non_retryable_error_types=["BookingDeclined"],
)
```

```python
tool_options = {
    "start_to_close_timeout": timedelta(seconds=30),
    "retry_policy": TOOL_RETRY,
}
```

**What you need to understand:**

- Model calls get 4 attempts with a 2-minute timeout each. A Gemini outage longer than the backoff fails the turn, and the agent replies with what went wrong instead of hanging.
- Tool calls have no attempt limit, so a database outage just delays the turn until the database is back. That's beat 6.
- `LLMFatalError` and `BookingDeclined` stop retries straight away. The interceptor turns Gemini's "API key not valid" response into `LLMFatalError`, so a bad key fails in one attempt with a readable message. Other Gemini errors, such as rate limits, stay retryable.
- None of these Activities heartbeat, so after a Worker crash the Start-To-Close Timeout is what triggers the retry. A customer with long model calls would add a Heartbeat Timeout to detect a lost Worker sooner.

**Docs:** [Retry Policies](https://docs.temporal.io/encyclopedia/retry-policies), [Activity timeouts](https://docs.temporal.io/encyclopedia/detecting-activity-failures)

### Run the research searches in parallel, each with its own retries

**When to use it:** during beat 2.

**Where it is:** [`python/workflows/agent.py` lines 177–216](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L177-L216) (`code -g python/workflows/agent.py:177`)

```python
async def research_destination(query: str) -> dict:
    """Run a deep, cited plan -> parallel web-search -> write pass."""
    self.research = ResearchStatus(phase="planning")
    plan: SearchPlan = await workflow.execute_activity(
        plan_searches,
        query,
        start_to_close_timeout=timedelta(seconds=90),
        retry_policy=MODEL_RETRY,
        summary="Plan destination research",
    )
    # ...
    self.research.phase = "searching"
    handles = [
        workflow.start_activity(
            web_search,
            item,
            start_to_close_timeout=timedelta(seconds=120),
            retry_policy=MODEL_RETRY,
            summary=f"Search: {item.query}",
        )
        for item in plan.searches
    ]
    findings: list[str] = []
    for handle in handles:
        findings.append(await handle)
        self.research.searches_done += 1
    self.research.phase = "writing"
    report: ReportData = await workflow.execute_activity(
        write_report,
        # ...
    )
```

**What you need to understand:**

- `start_activity` schedules every search before the loop awaits any of them, so all of them run at once. The Workflow then collects results in plan order.
- ADK calls this tool like any other, but the tool is Workflow code, so its three steps are three kinds of Activity, each retried on its own.
- `self.research` is plain Workflow state. The progress card reads it through the `research_status` Query; there's no progress table anywhere.
- The planner asks for `RESEARCH_SEARCHES` searches, 6 by default. The **n/6** counter counts completed searches in plan order, so a slow first search holds the counter even when later ones are done.

### Keep the conversation in one long-running Workflow

**When to use it:** after beat 3, or when someone asks where the chat state lives.

**Where it is:** [`python/workflows/agent.py` lines 114–125](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L114-L125) (`code -g python/workflows/agent.py:114`) and the `send_message` Update in [lines 279–304](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L279-L304)

```python
@workflow.run
async def run(self, traveller_email: str) -> None:
    self.account_key = workflow.info().workflow_id
    agent = self._build_agent(traveller_email)
    self.runner = InMemoryRunner(agent=agent, app_name="travel_planner")
    await self.runner.session_service.create_session(
        app_name="travel_planner",
        user_id=self.account_key,
        session_id=self.session_id,
        state={"account_key": self.account_key},
    )
    await workflow.wait_condition(lambda: False)
```

```python
@workflow.update(name="send_message")
async def send_message(self, text: str) -> TurnResult:
    if self.turn_in_progress:
        raise ApplicationError(
            "A turn is already in progress.",
            type="TurnInProgress",
            non_retryable=True,
        )
    # ...
    try:
        await workflow.wait_condition(lambda: not self.llm_down)
        answer, pending = await self._run_adk_pass(
            types.Content(role="user", parts=[types.Part(text=text)])
        )
```

**What you need to understand:**

- One conversation is one Workflow execution, and the Workflow ID is the conversation ID. The Workflow never returns; it waits and serves Updates, Signals, and Queries.
- Each chat message is an Update. The gateway's HTTP call waits for the Update's result, so the reply comes back on the same request, and a turn that's already running rejects a second message with a 409.
- `InMemoryRunner` sounds fragile, but its memory is rebuilt by replay. The transcript, the itinerary, and the ADK session survive a crash because they're derived from the Workflow's history.
- The `wait_condition` on `llm_down` is the outage switch from beat 6. A message sent during the outage waits there without calling the model.
- Limits: the Workflow never continues-as-new, so its history grows with every turn. A production chat that lasts weeks would need Continue-As-New and a way to carry the session across. Reloading the browser starts a new conversation; the old Workflow keeps running, idle.

**Docs:** [Python: Updates](https://docs.temporal.io/develop/python/workflows/message-passing#updates), [Python: Continue-As-New](https://docs.temporal.io/develop/python/workflows/continue-as-new)

### Bridge ADK's tool confirmation to a Temporal Signal

**When to use it:** during beat 4, or when someone asks how the approval survives a restart.

**Where it is:** [`python/workflows/agent.py` lines 297–302](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L297-L302), [lines 365–383](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L365-L383), and [lines 401–403](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L401-L403) (`code -g python/workflows/agent.py:297`)

```python
if pending is not None:
    self.pending_confirmation = self._confirmation_view(pending)
    self._approval_task = asyncio.create_task(
        self._resume_after_approval(pending["confirmation_id"])
    )
    return TurnResult(status="awaiting_approval", reply=answer)
```

```python
async def _resume_after_approval(self, confirmation_id: str) -> None:
    try:
        await workflow.wait_condition(lambda: self.approval is not None)
        # ...
        response = types.Content(
            role="user",
            parts=[types.Part(function_response=types.FunctionResponse(
                id=confirmation_id,
                name="adk_request_confirmation",
                response={
                    "confirmed": decision.approved,
                    "payload": {"reason": decision.reason},
                },
            ))],
        )
        answer, pending = await self._run_adk_pass(response)
```

```python
@workflow.signal(name="confirm_action")
def confirm_action(self, decision: ApprovalDecision) -> None:
    self.approval = decision
```

**What you need to understand:**

- `require_confirmation=True` on `book_trip` and `create_invoice` is ADK's own feature. When the model calls one of them, ADK emits an `adk_request_confirmation` call instead of running the tool. The Workflow spots it and stops the ADK pass there.
- The Update returns `awaiting_approval` straight away, so the browser isn't held open. A background `asyncio` task inside the Workflow then waits for the `confirm_action` Signal, which the gateway sends when you press **Confirm** or **Cancel**.
- When the Signal arrives, the Workflow hands ADK the confirmation as a function response, and ADK runs the tool or reports it rejected.
- The wait has no timeout here. A customer would usually add one, for example `wait_condition(..., timeout=timedelta(days=2))`, and decline when it fires.

**Docs:** [Workflow message passing](https://docs.temporal.io/encyclopedia/workflow-message-passing), [Human-in-the-loop AI agent](https://docs.temporal.io/ai/cookbook/human-in-the-loop-python)

### Undo completed bookings when a later one fails

**When to use it:** during beat 5.

**Where it is:** [`python/workflows/checkout.py` lines 87–107](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/checkout.py#L87-L107) (`code -g python/workflows/checkout.py:87`), with the retry policy in [lines 32–38](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/checkout.py#L32-L38). The agent starts it in [`python/workflows/agent.py` lines 236–245](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/workflows/agent.py#L236-L245).

```python
CHECKOUT_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=1),
    backoff_coefficient=2.0,
    maximum_interval=timedelta(seconds=5),
    maximum_attempts=3,
    non_retryable_error_types=["HotelBookingFailed", "BookingDeclined"],
)
```

```python
except ActivityError as error:
    failure = _failure_message(error)
    compensations: list[CheckoutReservation] = []

    # Saga compensation: undo only the side effects that completed,
    # in reverse order. On the demo path the hotel fails after the
    # flight succeeds, so this schedules a visible cancel_flight.
    for reservation in reversed(reservations):
        cancel_activity_for_kind = {
            "flight": cancel_flight,
            "hotel": cancel_hotel,
            "activity": cancel_activity,
        }[reservation.kind]
        compensation = await workflow.execute_activity(
            cancel_activity_for_kind,
            reservation,
            start_to_close_timeout=timedelta(seconds=30),
            retry_policy=CHECKOUT_RETRY,
            summary=f"Cancel {reservation.kind}: {reservation.title}",
        )
        compensations.append(compensation)
```

**What you need to understand:**

- The booking loop above this books flights, then hotels, then activities, and appends each reservation as it succeeds. The `except` cancels exactly those, newest first. It's an ordinary `try`/`except`; Temporal makes sure it finishes even if the Worker restarts halfway.
- `HotelBookingFailed` is non-retryable, so a sold-out hotel fails on the first attempt instead of three. Other errors get 3 attempts.
- The child returns `compensated` instead of failing, so the conversation Workflow stays alive and ADK explains the result to the traveller.
- The child's ID is `<conversation>-checkout-<n>`, so each booking attempt is a separate, findable execution.
- Each reservation ID is a hash of the conversation, item kind, and item ID, which stands in for the idempotency key a real supplier API would take. A retried booking can't make a second reservation.
- If a cancellation itself keeps failing, it gives up after 3 attempts and the child Workflow fails. A customer would usually retry compensations without a limit.

**Docs:** [Saga pattern](https://docs.temporal.io/design-patterns/saga-pattern), [Child Workflows](https://docs.temporal.io/child-workflows)

### What's demo-only

When someone asks what they'd change for their own system, these are the parts to leave out:

- **The hotel failure.** [`python/activities/checkout.py` lines 51–61](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/activities/checkout.py#L51-L61): `book_hotel` raises `HotelBookingFailed` whenever `CHECKOUT_FAIL_HOTEL` is on. Set it to `false` for a successful checkout.
- **The flaky searches.** [`python/activities/research.py` lines 75–82](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/activities/research.py#L75-L82): the first attempt of each search fails at `WEB_SEARCH_FAIL_RATE`. Set it to `0` to turn it off.
- **The pacing delays.** [`python/config.py` lines 59–71](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/config.py#L59-L71): each catalog tool sleeps `TOOL_DELAY_SECONDS` and each checkout step sleeps `CHECKOUT_STEP_DELAY_SECONDS`, 1 second each, so steps are visible and there's time to crash the Worker.
- **The outage switch.** [`python/activities/research.py` lines 22–27](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/activities/research.py#L22-L27): the switch is a `set_llm_status` Signal, and each research Activity queries its own Workflow (through [`python/activities/control.py`](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/python/activities/control.py)) and raises `LLMProviderDown` while it's on. A real outage needs neither.
- **The suppliers.** The booking and cancellation Activities return a reservation without calling anyone. Replace their bodies with real calls and the Workflow doesn't change.

### If someone asks about Go

The Go Worker implements the same Workflow with `go.temporal.io/sdk/contrib/googleadk`, and the gateway and UI don't change. The equivalents of the first entry are `googleadk.NewModel` in [`go/internal/travelagent/workflows.go` line 280](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/go/internal/travelagent/workflows.go#L280), `googleadk.ActivityAsTool` from [line 105](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/go/internal/travelagent/workflows.go#L105-L110), and `googleadk.NewPlugin` in [`go/cmd/worker/main.go` lines 30–36](https://github.com/temporal-sa/canonical-adk-demo/blob/51f9c8f34352ff518fdee0a32028eb3c997deb32/go/cmd/worker/main.go#L30-L36).

To switch locally, run `make kill-worker`, then `cd go && make worker`, and reload the page to start a new conversation. Only one SDK's Worker may poll `travel-agent` at a time, because a history written by one SDK can't be replayed by the other; `make worker` refuses to start while the other SDK's Worker runs.

## Reset and tear down

- **Between runs:** reload the page. The next message starts a new conversation Workflow with an empty itinerary. The old Workflow stays open and idle; it sends no Queries and schedules nothing.
- **To close old conversations** on a server you keep running:

  ```bash
  temporal workflow terminate \
    --query 'WorkflowType="TravelAgentWorkflow" AND ExecutionStatus="Running"' \
    --reason "demo reset" --yes
  ```

  Add `--address`, `--namespace`, and `--api-key` for Temporal Cloud.
- **To stop everything locally:** `make down`. It stops the Worker, the gateway, the web server, and the dev server, and removes the Postgres container. The dev server keeps nothing on disk, and the next `make up` reseeds the database, so invoices and bookings from earlier runs are gone.
- **On tmprl-demo.cloud:** press **Delete** on the crashable environment when you're done, or let it expire after an hour.

## When something goes wrong

| What you see | Likely cause | Fix |
|---|---|---|
| The reply says `Gemini rejected GOOGLE_API_KEY` | The key in `.env` is wrong | Fix `GOOGLE_API_KEY` in `.env`, then `make kill-worker` and `make worker` |
| A reply starts with `I couldn't complete that turn:` | A model call failed 4 times, or failed with a non-retryable error | Read the failed Activity in the Temporal UI and `/tmp/travel-worker.log`; then send the message again |
| The typing bubble never goes away | The Worker isn't running, or the **LLM API** switch is off | `make status`; check that the **Demo controls** dot isn't amber |
| An error toast says `A turn is already in progress.` | A turn, or an approval card, is still open | Wait for the reply, or answer the approval card |
| **Confirm** shows an error | The Worker is down; the button runs a Query first, and Queries need a Worker | Start the Worker, then press **Confirm** again |
| `Timed out waiting for the agent — check the worker.` after **Confirm** | The page stops polling 90 seconds after an approval | Start the Worker if it's down. The Workflow still finishes the turn; run the `transcript` Query from the Workflow's **Queries** tab in the Temporal UI to read the reply, or reload to start over |
| The turn waits a minute or two after a crash | The in-flight Activity hasn't reached its Start-To-Close Timeout yet | Expected; see beat 3 |
| `make worker` says another SDK's worker is running | The Go Worker is still polling `travel-agent` | `cd go && make kill-worker`, then `make worker` from the root |
| **Demo controls** shows `claude-sonnet-4-6` | You opened the static server on port 5173, which serves a placeholder `config.js` | Use http://localhost:8000 |

## Keeping this runbook current

The code links point at a fixed commit. When a linked file changes, update its link, line numbers, and snippet together. Before an event, list the linked files that have changed:

```sh
grep -oE 'blob/[0-9a-f]{40}/[^#)]+' RUNBOOK.md | sort -u | while IFS=/ read -r _ sha file; do
  git diff --quiet "$sha" HEAD -- "$file" || echo "changed since ${sha:0:7}: $file"
done
```
