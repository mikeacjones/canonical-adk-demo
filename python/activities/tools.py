"""Database-backed Google ADK tools, executed as Temporal Activities.

Each public function has the exact name and typed signature exposed to the ADK
model. ``activity_as_tool`` keeps that schema but dispatches the implementation
outside Workflow code, where Postgres I/O and retries belong.
"""

import time

from temporalio import activity

import config
from models.types import ItineraryItem
from . import db


def _pause() -> None:
    if config.TOOL_DELAY_SECONDS > 0:
        time.sleep(config.TOOL_DELAY_SECONDS)


@activity.defn
def search_events(destination: str, month: str | None = None) -> list[dict]:
    """Find festivals, concerts, sports, and conferences at a destination."""
    _pause()
    return db.search_events(destination, month)


@activity.defn
def search_destinations(query: str) -> list[dict]:
    """Find destinations by city, country, region, or interest."""
    _pause()
    return db.search_destinations(query)


@activity.defn
def get_destination_info(destination: str) -> dict:
    """Get quick facts and top attractions for one destination."""
    _pause()
    return db.get_destination_info(destination)


@activity.defn
def search_flights(
    destination: str,
    origin: str | None = None,
    depart_date: str | None = None,
) -> list[dict]:
    """Find flights to a destination, optionally from an origin and on a date."""
    _pause()
    return db.search_flights(destination, origin, depart_date)


@activity.defn
def search_hotels(destination: str, max_price: float | None = None) -> list[dict]:
    """Find hotels at a destination, optionally under a nightly price."""
    _pause()
    return db.search_hotels(destination, max_price)


@activity.defn
def search_attractions(destination: str) -> list[dict]:
    """List attractions and activities at a destination."""
    _pause()
    return db.search_attractions(destination)


@activity.defn
def get_bookings(account_key: str) -> list[dict]:
    """List the traveller's previously booked trips."""
    _pause()
    return db.get_bookings(account_key)


@activity.defn
def get_booking_details(booking_id: int) -> list[dict]:
    """Get the items and status for a previous booking."""
    _pause()
    return db.get_booking_details(booking_id)


@activity.defn
def resolve_itinerary_items(items: list[dict]) -> list[ItineraryItem]:
    """Resolve stable flight, hotel, and activity IDs into itinerary items."""
    _pause()
    return [ItineraryItem(**item) for item in db.get_itinerary_items(items)]


@activity.defn
def create_invoice_record(
    account_key: str,
    amount: float,
    flight_details: str,
) -> dict:
    """Persist an approved invoice for one selected flight."""
    _pause()
    return db.record_invoice(account_key, amount, flight_details)
