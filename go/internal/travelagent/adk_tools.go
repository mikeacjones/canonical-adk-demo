package travelagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// Concrete activity inputs are also the schemas Google ADK exposes to Gemini.
// The account identity and durable itinerary never appear in these structs;
// Workflow-owned tools add those values after the model call.
type SearchEventsArgs struct {
	Destination string `json:"destination"`
	Month       string `json:"month,omitempty"`
}

type SearchDestinationsArgs struct {
	Query string `json:"query"`
}

type DestinationInfoArgs struct {
	Destination string `json:"destination"`
}

type SearchFlightsArgs struct {
	Destination string `json:"destination"`
	Origin      string `json:"origin,omitempty"`
	DepartDate  string `json:"depart_date,omitempty"`
}

type SearchHotelsArgs struct {
	Destination string   `json:"destination"`
	MaxPrice    *float64 `json:"max_price,omitempty"`
}

type SearchAttractionsArgs struct {
	Destination string `json:"destination"`
}

type BookingDetailsArgs struct {
	BookingID int64 `json:"booking_id"`
}

type ItineraryRef struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

type ResolveItineraryArgs struct {
	Items []ItineraryRef `json:"items"`
}

type AccountArgs struct {
	AccountKey string `json:"account_key"`
}

type InvoiceRecordArgs struct {
	AccountKey    string  `json:"account_key"`
	Amount        float64 `json:"amount"`
	FlightDetails string  `json:"flight_details"`
}

// These declarations are never registered directly. ActivityAsTool reads their
// signatures to build Gemini's JSON schemas and dispatches by the explicit tool
// name; the worker registers the matching bound Activities methods below.
func searchEventsActivity(context.Context, SearchEventsArgs) ([]map[string]any, error) {
	panic("workflow-side activity declaration")
}
func searchDestinationsActivity(context.Context, SearchDestinationsArgs) ([]map[string]any, error) {
	panic("workflow-side activity declaration")
}
func destinationInfoActivity(context.Context, DestinationInfoArgs) (map[string]any, error) {
	panic("workflow-side activity declaration")
}
func searchFlightsActivity(context.Context, SearchFlightsArgs) ([]map[string]any, error) {
	panic("workflow-side activity declaration")
}
func searchHotelsActivity(context.Context, SearchHotelsArgs) ([]map[string]any, error) {
	panic("workflow-side activity declaration")
}
func searchAttractionsActivity(context.Context, SearchAttractionsArgs) ([]map[string]any, error) {
	panic("workflow-side activity declaration")
}
func bookingDetailsActivity(context.Context, BookingDetailsArgs) ([]map[string]any, error) {
	panic("workflow-side activity declaration")
}

func (a *Activities) pauseTool() {
	if a.cfg.ToolDelay > 0 {
		time.Sleep(a.cfg.ToolDelay)
	}
}

func (a *Activities) SearchEvents(ctx context.Context, in SearchEventsArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.SearchEvents(ctx, in.Destination, in.Month)
}

func (a *Activities) SearchDestinations(ctx context.Context, in SearchDestinationsArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.SearchDestinations(ctx, in.Query)
}

func (a *Activities) GetDestinationInfo(ctx context.Context, in DestinationInfoArgs) (map[string]any, error) {
	a.pauseTool()
	return a.db.GetDestinationInfo(ctx, in.Destination)
}

func (a *Activities) SearchFlights(ctx context.Context, in SearchFlightsArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.SearchFlights(ctx, in.Destination, in.Origin, in.DepartDate)
}

func (a *Activities) SearchHotels(ctx context.Context, in SearchHotelsArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.SearchHotels(ctx, in.Destination, in.MaxPrice)
}

func (a *Activities) SearchAttractions(ctx context.Context, in SearchAttractionsArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.SearchAttractions(ctx, in.Destination)
}

func (a *Activities) GetBookings(ctx context.Context, in AccountArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.GetBookings(ctx, in.AccountKey)
}

func (a *Activities) GetBookingDetails(ctx context.Context, in BookingDetailsArgs) ([]map[string]any, error) {
	a.pauseTool()
	return a.db.GetBookingDetails(ctx, in.BookingID)
}

func (a *Activities) ResolveItineraryItems(ctx context.Context, in ResolveItineraryArgs) ([]ItineraryItem, error) {
	a.pauseTool()
	items := make([]map[string]any, 0, len(in.Items))
	for _, item := range in.Items {
		items = append(items, map[string]any{"kind": item.Kind, "id": item.ID})
	}
	return a.db.GetItineraryItems(ctx, items)
}

func (a *Activities) CreateInvoiceRecord(ctx context.Context, in InvoiceRecordArgs) (map[string]any, error) {
	a.pauseTool()
	return a.db.RecordInvoice(ctx, in.AccountKey, in.Amount, in.FlightDetails)
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func (a *Activities) gemini(ctx context.Context, body map[string]any) (string, error) {
	if a.llmDown(ctx) {
		return "", temporal.NewApplicationError("Gemini is unavailable (simulated outage).", "LLMProviderDown")
	}
	if a.cfg.GoogleAPIKey == "" {
		return "", temporal.NewNonRetryableApplicationError(
			"GOOGLE_API_KEY or GEMINI_API_KEY is required", "LLMFatalError", nil,
		)
	}
	model := url.PathEscape(a.cfg.ADKModel)
	endpoint := strings.TrimRight(a.cfg.GeminiAPIBase, "/") + "/models/" + model + ":generateContent"
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-goog-api-key", a.cfg.GoogleAPIKey)
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("Gemini returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
			return "", temporal.NewNonRetryableApplicationError(message, "LLMFatalError", nil)
		}
		return "", fmt.Errorf("%s", message)
	}
	var decoded geminiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("decode Gemini response: %w", err)
	}
	var text strings.Builder
	for _, candidate := range decoded.Candidates {
		for _, part := range candidate.Content.Parts {
			text.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(text.String()), nil
}

func (a *Activities) PlanSearchesGemini(ctx context.Context, brief string) (SearchPlan, error) {
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": PlanSystem(a.cfg.ResearchSearches)}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Research brief:\n" + brief}}}},
		"generationConfig": map[string]any{
			"temperature": 0.2, "responseMimeType": "application/json", "responseJsonSchema": PlanSchema,
		},
	}
	text, err := a.gemini(ctx, body)
	if err != nil {
		return SearchPlan{}, err
	}
	var plan SearchPlan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		return SearchPlan{}, fmt.Errorf("decode research plan: %w", err)
	}
	return plan, nil
}

func (a *Activities) WebSearchGemini(ctx context.Context, item SearchItem) (string, error) {
	if activity.GetInfo(ctx).Attempt == 1 && rand.Float64() < a.cfg.WebSearchFailRate {
		return "", temporal.NewApplicationError(
			"web_search transient failure (simulated) for "+item.Query,
			"WebSearchFlaky",
		)
	}
	prompt := fmt.Sprintf(
		"Search query: %s\nWhy this matters: %s\n\nSearch the web and summarize the most relevant findings with source URLs.",
		item.Query, item.Reason,
	)
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": SearchSystem}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
		"tools":             []any{map[string]any{"googleSearch": map[string]any{}}},
		"generationConfig":  map[string]any{"temperature": 0.2},
	}
	text, err := a.gemini(ctx, body)
	if err != nil {
		return "", err
	}
	if text == "" {
		text = "(no findings)"
	}
	return "### " + item.Query + "\n" + text, nil
}

func (a *Activities) WriteReportGemini(ctx context.Context, in WriteRequest) (ReportData, error) {
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": WriteSystem}}},
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{
			"text": "Research brief:\n" + in.Brief + "\n\nFindings from web searches:\n\n" + strings.Join(in.Findings, "\n\n"),
		}}}},
		"generationConfig": map[string]any{
			"temperature": 0.2, "responseMimeType": "application/json", "responseJsonSchema": WriteSchema,
		},
	}
	text, err := a.gemini(ctx, body)
	if err != nil {
		return ReportData{}, err
	}
	var report ReportData
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		return ReportData{}, fmt.Errorf("decode research report: %w", err)
	}
	return report, nil
}
