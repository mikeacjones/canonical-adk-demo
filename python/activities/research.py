"""Durable Gemini research steps used by the ADK research tool.

The ADK agent decides when a deep pass is appropriate. The Workflow-backed
tool then schedules these Activities as plan -> parallel Google Search -> write,
so every external call is independently retryable and visible in Temporal.
"""

import json
import random

from google import genai
from google.genai import types
from temporalio import activity
from temporalio.exceptions import ApplicationError

import config
import prompts
from . import control
from models.types import ReportData, SearchItem, SearchPlan, WriteRequest


async def _guard() -> None:
    if await control.llm_down():
        raise ApplicationError(
            "Gemini is unavailable (simulated outage).",
            type="LLMProviderDown",
        )
    if not config.GOOGLE_API_KEY:
        raise ApplicationError(
            "GOOGLE_API_KEY or GEMINI_API_KEY is required",
            type="LLMFatalError",
            non_retryable=True,
        )


def _client() -> genai.Client:
    return genai.Client(api_key=config.GOOGLE_API_KEY)


async def _structured(system: str, user: str, schema: dict) -> dict:
    try:
        response = await _client().aio.models.generate_content(
            model=config.ADK_MODEL,
            contents=user,
            config=types.GenerateContentConfig(
                system_instruction=system,
                response_mime_type="application/json",
                response_json_schema=schema,
                temperature=0.2,
            ),
        )
    except Exception as error:
        status = getattr(error, "status_code", None) or getattr(error, "code", None)
        if status in (400, 401, 403, 404):
            raise ApplicationError(
                f"Gemini request rejected: {error}",
                type="LLMFatalError",
                non_retryable=True,
            ) from error
        raise
    return json.loads(response.text or "{}")


@activity.defn
async def plan_searches(brief: str) -> SearchPlan:
    await _guard()
    data = await _structured(
        prompts.plan_system(config.RESEARCH_SEARCHES),
        f"Research brief:\n{brief}",
        prompts.PLAN_SCHEMA,
    )
    return SearchPlan(searches=[SearchItem(**item) for item in data.get("searches", [])])


@activity.defn
async def web_search(item: SearchItem) -> str:
    await _guard()
    if activity.info().attempt == 1 and random.random() < config.WEB_SEARCH_FAIL_RATE:
        raise ApplicationError(
            f"web_search transient failure (simulated) for {item.query!r}",
            type="WebSearchFlaky",
        )
    prompt = (
        f"Search query: {item.query}\n"
        f"Why this matters: {item.reason}\n\n"
        "Search the web and summarize the most relevant findings with source URLs."
    )
    response = await _client().aio.models.generate_content(
        model=config.ADK_MODEL,
        contents=prompt,
        config=types.GenerateContentConfig(
            system_instruction=prompts.SEARCH_SYSTEM,
            tools=[types.Tool(google_search=types.GoogleSearch())],
            temperature=0.2,
        ),
    )
    text = (response.text or "").strip() or "(no findings)"
    return f"### {item.query}\n{text}"


@activity.defn
async def write_report(request: WriteRequest) -> ReportData:
    await _guard()
    findings = "\n\n".join(request.findings)
    data = await _structured(
        prompts.WRITE_SYSTEM,
        f"Research brief:\n{request.brief}\n\nFindings from web searches:\n\n{findings}",
        prompts.WRITE_SCHEMA,
    )
    return ReportData(
        short_summary=data.get("short_summary", ""),
        markdown_report=data.get("markdown_report", ""),
    )
