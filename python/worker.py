"""Python worker for the durable Google ADK travel planner."""

import asyncio
from concurrent.futures import ThreadPoolExecutor

from temporalio.contrib.google_adk_agents import GoogleAdkPlugin
from temporalio.worker import Worker

import config
from activities import control
from activities.checkout import (
    book_activity,
    book_flight,
    book_hotel,
    cancel_activity,
    cancel_flight,
    cancel_hotel,
    finalize_checkout,
)
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
from workflows.agent import TravelAgentWorkflow
from workflows.checkout import CheckoutWorkflow


async def main() -> None:
    client = await config.temporal_client(plugins=[GoogleAdkPlugin()])
    control.set_client(client)
    with ThreadPoolExecutor(max_workers=12) as activity_executor:
        worker = Worker(
            client,
            task_queue=config.TASK_QUEUE,
            workflows=[TravelAgentWorkflow, CheckoutWorkflow],
            activities=[
                search_events,
                search_destinations,
                get_destination_info,
                search_flights,
                search_hotels,
                search_attractions,
                get_bookings,
                get_booking_details,
                resolve_itinerary_items,
                create_invoice_record,
                plan_searches,
                web_search,
                write_report,
                book_flight,
                book_hotel,
                book_activity,
                cancel_flight,
                cancel_hotel,
                cancel_activity,
                finalize_checkout,
            ],
            activity_executor=activity_executor,
        )
        print(
            f"python Google ADK worker polling '{config.TASK_QUEUE}' on "
            f"{config.TEMPORAL_ADDRESS} (model: {config.ADK_MODEL})"
        )
        await worker.run()


if __name__ == "__main__":
    asyncio.run(main())
