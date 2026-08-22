import inspect
import unittest

from temporalio.contrib.google_adk_agents.workflow import activity_tool

from activities.tools import search_flights
from models.types import ItineraryItem
from workflows.agent import TravelAgentWorkflow


class GoogleAdkContractTests(unittest.TestCase):
    def test_agent_builds_with_released_adk_and_temporal_integration(self) -> None:
        workflow = TravelAgentWorkflow("traveller@example.com")
        workflow.account_key = "trip-test"

        agent = workflow._build_agent("traveller@example.com")

        self.assertEqual("travel_planner", agent.name)
        self.assertEqual(13, len(agent.tools))

    def test_activity_tool_preserves_the_typed_schema(self) -> None:
        tool = activity_tool(search_flights)
        signature = inspect.signature(tool)

        self.assertEqual("search_flights", tool.__name__)
        self.assertEqual(
            ["destination", "origin", "depart_date"],
            list(signature.parameters),
        )

    def test_confirmation_view_matches_the_shared_gateway_contract(self) -> None:
        workflow = TravelAgentWorkflow("traveller@example.com")
        workflow.itinerary = [
            ItineraryItem(
                kind="flight",
                ref_id=7,
                title="Demo Air 7",
                subtitle="SFO → HND",
                price=321.45,
            )
        ]

        invoice = workflow._confirmation_view(
            {
                "name": "create_invoice",
                "args": {"amount": 321.45, "flight_details": "Demo Air 7"},
            }
        )
        booking = workflow._confirmation_view({"name": "book_trip", "args": {}})

        self.assertEqual("create_invoice", invoice.action)
        self.assertEqual(321.45, invoice.amount)
        self.assertEqual("book_trip", booking.action)
        self.assertEqual(321.45, booking.amount)
        self.assertEqual("1 item(s) — $321.45", booking.detail)


if __name__ == "__main__":
    unittest.main()
