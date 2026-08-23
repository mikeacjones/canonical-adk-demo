"""Long-lived travel chat powered by Google ADK and made durable by Temporal.

ADK owns the model/tool loop. Temporal's Google ADK integration moves model
calls into Activities, while this Workflow owns the conversation lifetime,
approval signals, durable itinerary, research fan-out, and checkout child.
"""

from __future__ import annotations

import asyncio
from datetime import timedelta
from typing import Any

from temporalio import workflow
from temporalio.common import RetryPolicy
from temporalio.exceptions import ApplicationError
from temporalio.workflow import ActivityConfig

with workflow.unsafe.imports_passed_through():
    import config
    from google.adk import Agent
    from google.adk.tools.function_tool import FunctionTool
    from google.adk.runners import InMemoryRunner
    from google.adk.utils.context_utils import Aclosing
    from google.genai import types
    from temporalio.contrib.google_adk_agents import TemporalModel
    from temporalio.contrib.google_adk_agents.workflow import activity_tool

    from activities.research import plan_searches, web_search, write_report
    from activities.tools import (
        create_invoice_record,
        get_booking_details,
        get_bookings,
        get_destination_info,
        resolve_itinerary_items,
        search_attractions,
        search_destinations,
        search_events,
        search_flights,
        search_hotels,
    )
    from models.types import (
        ApprovalDecision,
        ChatMessage,
        CheckoutRequest,
        ItineraryItem,
        PendingConfirmation,
        ReportData,
        ResearchStatus,
        SearchPlan,
        TurnResult,
        WriteRequest,
    )
    from prompts import system_prompt
    from workflows.checkout import CheckoutWorkflow


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


def _turn_failure_message(error: BaseException, *, approved: bool = False) -> str:
    """Return a concise chat-safe diagnosis from a nested Temporal failure."""
    current: BaseException | None = error
    seen: set[int] = set()
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        message = getattr(current, "message", None) or str(current)
        if (
            "API_KEY_INVALID" in message
            or "API key not valid" in message
            or "Gemini rejected GOOGLE_API_KEY" in message
        ):
            return (
                "Gemini rejected GOOGLE_API_KEY. Replace it in the repository-root "
                ".env and restart the worker."
            )
        current = getattr(current, "cause", None)
    action = "finish the approved action" if approved else "complete that turn"
    return f"I couldn't {action}: {type(error).__name__}: {error}"


@workflow.defn
class TravelAgentWorkflow:
    """One Workflow execution owns one ADK session and one browser conversation."""

    @workflow.init
    def __init__(self, traveller_email: str) -> None:
        self.traveller_email = traveller_email
        self.account_key = ""
        self.transcript_messages: list[ChatMessage] = []
        self.pending_confirmation: PendingConfirmation | None = None
        self.approval: ApprovalDecision | None = None
        self.turn_in_progress = False
        self.llm_down = False
        self.checkout_attempt = 0
        self.itinerary: list[ItineraryItem] = []
        self.research = ResearchStatus()
        self.runner: InMemoryRunner | None = None
        self.session_id = "travel-session"
        self._approval_task: asyncio.Task[None] | None = None

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

    def _build_agent(self, traveller_email: str) -> Agent:
        tool_options = {
            "start_to_close_timeout": timedelta(seconds=30),
            "retry_policy": TOOL_RETRY,
        }

        async def get_previous_bookings() -> list[dict]:
            """List the signed-in traveller's previously booked trips."""
            return await workflow.execute_activity(
                get_bookings,
                self.account_key,
                summary="Get previous bookings",
                **tool_options,
            )

        get_previous_bookings.__name__ = "get_bookings"

        async def add_to_itinerary(items: list[dict]) -> dict:
            """Add stable flight, hotel, and activity IDs to the itinerary."""
            resolved = await workflow.execute_activity(
                resolve_itinerary_items,
                items,
                summary="Resolve itinerary items",
                **tool_options,
            )
            existing = {item.item_id for item in self.itinerary}
            added: list[dict] = []
            for item in resolved:
                if item.item_id in existing:
                    continue
                self.itinerary.append(item)
                existing.add(item.item_id)
                added.append({"item_id": item.item_id, "title": item.title})
            return {
                "added": added,
                "itinerary_size": len(self.itinerary),
                "itinerary_total": round(sum(item.price for item in self.itinerary), 2),
            }

        async def remove_from_itinerary(item_ids: list[str]) -> dict:
            """Remove itinerary entries by item_id, such as flight-12."""
            before = len(self.itinerary)
            remove = set(item_ids)
            self.itinerary = [item for item in self.itinerary if item.item_id not in remove]
            return {
                "removed": before - len(self.itinerary),
                "itinerary_size": len(self.itinerary),
                "itinerary_total": round(sum(item.price for item in self.itinerary), 2),
            }

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
            self.research.plan = plan.searches
            self.research.searches_total = len(plan.searches)
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
                WriteRequest(brief=query, findings=findings),
                start_to_close_timeout=timedelta(seconds=180),
                retry_policy=MODEL_RETRY,
                summary="Write cited destination guide",
            )
            self.research.phase = "idle"
            return {
                "short_summary": report.short_summary,
                "markdown_report": report.markdown_report,
            }

        async def create_invoice(amount: float, flight_details: str) -> dict:
            """Create an approved invoice for one selected flight."""
            if amount <= 0 or not flight_details:
                return {"error": "A positive amount and flight details are required."}
            return await workflow.execute_activity(
                create_invoice_record,
                args=[self.account_key, round(amount, 2), flight_details],
                summary="Create approved flight invoice",
                **tool_options,
            )

        async def book_trip() -> dict:
            """Book the durable itinerary through a compensating child Workflow."""
            if not self.itinerary:
                return {"error": "The itinerary is empty."}
            total = round(sum(item.price for item in self.itinerary), 2)
            summary = f"{len(self.itinerary)} item(s) — ${total:,.2f}"
            self.checkout_attempt += 1
            result = await workflow.execute_child_workflow(
                CheckoutWorkflow.run,
                CheckoutRequest(
                    account_key=self.account_key,
                    items=list(self.itinerary),
                    summary=summary,
                ),
                id=f"{self.account_key}-checkout-{self.checkout_attempt}",
                static_summary="ADK-invoked durable checkout",
            )
            if result.status == "booked":
                self.itinerary = []
            return result.model_dump()

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
                activity_tool(search_destinations, summary="Search destinations", **tool_options),
                activity_tool(get_destination_info, summary="Get destination info", **tool_options),
                activity_tool(search_flights, summary="Search flights", **tool_options),
                activity_tool(search_hotels, summary="Search hotels", **tool_options),
                activity_tool(search_attractions, summary="Search attractions", **tool_options),
                FunctionTool(func=get_previous_bookings),
                activity_tool(get_booking_details, summary="Get booking details", **tool_options),
                FunctionTool(func=add_to_itinerary),
                FunctionTool(func=remove_from_itinerary),
                FunctionTool(func=research_destination),
                FunctionTool(func=create_invoice, require_confirmation=True),
                FunctionTool(func=book_trip, require_confirmation=True),
            ],
        )

    @workflow.update(name="send_message")
    async def send_message(self, text: str) -> TurnResult:
        if self.turn_in_progress:
            raise ApplicationError(
                "A turn is already in progress.",
                type="TurnInProgress",
                non_retryable=True,
            )
        if not text.strip():
            raise ApplicationError("Message must not be empty.", non_retryable=True)
        self.turn_in_progress = True
        self.approval = None
        self.transcript_messages.append(ChatMessage(role="user", content=text))
        try:
            await workflow.wait_condition(lambda: not self.llm_down)
            answer, pending = await self._run_adk_pass(
                types.Content(role="user", parts=[types.Part(text=text)])
            )
            if pending is not None:
                self.pending_confirmation = self._confirmation_view(pending)
                self._approval_task = asyncio.create_task(
                    self._resume_after_approval(pending["confirmation_id"])
                )
                return TurnResult(status="awaiting_approval", reply=answer)
            self._finish_turn(answer)
            return TurnResult(status="reply", reply=answer)
        except Exception as error:
            message = _turn_failure_message(error)
            self._finish_turn(message)
            return TurnResult(status="reply", reply=message)

    async def _run_adk_pass(
        self, message: types.Content
    ) -> tuple[str, dict[str, Any] | None]:
        assert self.runner is not None
        answer = ""
        pending: dict[str, Any] | None = None
        async with Aclosing(
            self.runner.run_async(
                user_id=self.account_key,
                session_id=self.session_id,
                new_message=message,
            )
        ) as events:
            async for event in events:
                if not event.content or not event.content.parts:
                    continue
                for part in event.content.parts:
                    if part.text:
                        answer = part.text
                    call = part.function_call
                    if call and call.name == "adk_request_confirmation":
                        original = (call.args or {}).get("originalFunctionCall", {})
                        pending = {
                            "confirmation_id": call.id,
                            "name": original.get("name", ""),
                            "args": original.get("args", {}),
                        }
                if pending is not None:
                    break
        return answer, pending

    def _confirmation_view(self, pending: dict[str, Any]) -> PendingConfirmation:
        name = pending["name"]
        args = pending["args"]
        if name == "create_invoice":
            amount = round(float(args.get("amount") or 0), 2)
            details = str(args.get("flight_details") or "")
            return PendingConfirmation(
                action="create_invoice",
                title="Create invoice",
                detail=details,
                amount=amount,
                args={"amount": amount, "flight_details": details},
            )
        total = round(sum(item.price for item in self.itinerary), 2)
        return PendingConfirmation(
            action="book_trip",
            title="Booking approval required",
            detail=f"{len(self.itinerary)} item(s) — ${total:,.2f}",
            amount=total,
            args={"items": [
                {"title": item.title, "price": item.price} for item in self.itinerary
            ]},
        )

    async def _resume_after_approval(self, confirmation_id: str) -> None:
        try:
            await workflow.wait_condition(lambda: self.approval is not None)
            assert self.approval is not None
            decision = self.approval
            self.approval = None
            self.pending_confirmation = None
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
            if pending is not None:
                self.pending_confirmation = self._confirmation_view(pending)
                self._approval_task = asyncio.create_task(
                    self._resume_after_approval(pending["confirmation_id"])
                )
                return
            self._finish_turn(answer)
        except Exception as error:
            self._finish_turn(_turn_failure_message(error, approved=True))

    def _finish_turn(self, answer: str) -> None:
        if answer:
            self.transcript_messages.append(ChatMessage(role="assistant", content=answer))
        self.research.phase = "idle"
        self.pending_confirmation = None
        self.turn_in_progress = False

    @workflow.signal(name="confirm_action")
    def confirm_action(self, decision: ApprovalDecision) -> None:
        self.approval = decision

    @workflow.signal(name="set_llm_status")
    def set_llm_status(self, down: bool) -> None:
        self.llm_down = down

    @workflow.query(name="is_llm_down")
    def is_llm_down(self) -> bool:
        return self.llm_down

    @workflow.query(name="transcript")
    def transcript(self) -> list[ChatMessage]:
        return self.transcript_messages

    @workflow.query(name="pending_approval")
    def pending_approval(self) -> PendingConfirmation | None:
        return self.pending_confirmation

    @workflow.query(name="research_status")
    def research_status(self) -> ResearchStatus:
        return self.research

    @workflow.query(name="itinerary_view")
    def itinerary_view(self) -> list[ItineraryItem]:
        return self.itinerary
