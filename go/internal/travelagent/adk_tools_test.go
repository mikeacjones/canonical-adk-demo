package travelagent

import (
	"testing"

	"go.temporal.io/sdk/contrib/googleadk"
	"google.golang.org/genai"
)

type declaredTool interface {
	Name() string
	Declaration() *genai.FunctionDeclaration
}

func TestAgentBuildsWithReleasedADKAndTemporalIntegration(t *testing.T) {
	agent, err := buildADKAgent(
		Config{ADKModel: "gemini-3.6-flash"},
		&agentState{accountKey: "trip-test"},
		"traveller@example.com",
	)
	if err != nil {
		t.Fatalf("buildADKAgent: %v", err)
	}
	if agent.Name() != "travel_planner" {
		t.Fatalf("agent name = %q", agent.Name())
	}
}

func TestActivityAsToolBuildsGeminiSchemaFromTypedActivity(t *testing.T) {
	wrapped, err := googleadk.ActivityAsTool(searchFlightsActivity, googleadk.ActivityToolOptions{
		Name:        "search_flights",
		Description: "Find flights",
	})
	if err != nil {
		t.Fatalf("ActivityAsTool: %v", err)
	}
	tool, ok := wrapped.(declaredTool)
	if !ok {
		t.Fatalf("wrapped tool does not expose a declaration: %T", wrapped)
	}
	if tool.Name() != "search_flights" {
		t.Fatalf("name = %q", tool.Name())
	}
	params := tool.Declaration().Parameters
	for _, field := range []string{"destination", "origin", "depart_date"} {
		if _, ok := params.Properties[field]; !ok {
			t.Errorf("Gemini schema is missing %q", field)
		}
	}
}

func TestConfirmationViewMatchesGatewayContract(t *testing.T) {
	s := &agentState{itinerary: []ItineraryItem{{
		Kind: "flight", RefID: 7, Title: "Demo Air 7", Price: 321.45,
	}}}
	invoice := confirmationView(s, googleadk.PendingConfirmation{OriginalCall: &genai.FunctionCall{
		Name: "create_invoice",
		Args: map[string]any{"amount": 321.45, "flight_details": "Demo Air 7"},
	}})
	booking := confirmationView(s, googleadk.PendingConfirmation{OriginalCall: &genai.FunctionCall{
		Name: "book_trip",
	}})

	if invoice.Action != "create_invoice" || invoice.Amount != 321.45 {
		t.Fatalf("invoice confirmation = %#v", invoice)
	}
	if booking.Action != "book_trip" || booking.Amount != 321.45 || booking.Detail != "1 item(s) — $321.45" {
		t.Fatalf("booking confirmation = %#v", booking)
	}
}
