package travelagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"go.temporal.io/sdk/contrib/googleadk"
)

type agentState struct {
	messages            []ChatMessage
	accountKey          string
	pendingConfirmation *PendingConfirmation
	approval            *ApprovalDecision
	turnInProgress      bool
	llmDown             bool
	checkoutAttempt     int
	itinerary           []ItineraryItem
	phase               string
	plan                []SearchItem
	searchesTotal       int
	searchesDone        int
	toolCtx             workflow.Context
}

type EmptyArgs struct{}
type RemoveItineraryArgs struct {
	ItemIDs []string `json:"item_ids"`
}
type ResearchDestinationArgs struct {
	Query string `json:"query"`
}
type CreateInvoiceArgs struct {
	Amount        float64 `json:"amount"`
	FlightDetails string  `json:"flight_details"`
}

// TravelAgentWorkflow is the same gateway-facing conversation contract as the
// canonical demo, but Google ADK owns the agent loop and tool dispatch.
func TravelAgentWorkflow(ctx workflow.Context, travellerEmail string) error {
	cfg := LoadConfig()
	s := &agentState{
		messages:   []ChatMessage{},
		accountKey: workflow.GetInfo(ctx).WorkflowExecution.ID,
		itinerary:  []ItineraryItem{},
		phase:      "idle",
		plan:       []SearchItem{},
		toolCtx:    ctx,
	}

	root, err := buildADKAgent(cfg, s, travellerEmail)
	if err != nil {
		return err
	}
	r, err := runner.New(runner.Config{
		AppName:           "travel_planner",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return err
	}

	if err := installHandlers(ctx, s, r); err != nil {
		return err
	}
	return workflow.Await(ctx, func() bool { return false })
}

func buildADKAgent(cfg Config, s *agentState, travellerEmail string) (agent.Agent, error) {
	toolOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     2,
			MaximumInterval:        10 * time.Second,
			NonRetryableErrorTypes: []string{"BookingDeclined"},
		},
	}
	modelOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     2,
			MaximumInterval:        10 * time.Second,
			MaximumAttempts:        4,
			NonRetryableErrorTypes: []string{"LLMFatalError"},
		},
	}

	searchEvents, err := googleadk.ActivityAsTool(searchEventsActivity, googleadk.ActivityToolOptions{
		Name: "search_events", Description: "Find festivals, concerts, sports, and conferences at a destination, optionally in a month.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}
	searchDestinations, err := googleadk.ActivityAsTool(searchDestinationsActivity, googleadk.ActivityToolOptions{
		Name: "search_destinations", Description: "Find destinations by city, country, region, or interest.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}
	destinationInfo, err := googleadk.ActivityAsTool(destinationInfoActivity, googleadk.ActivityToolOptions{
		Name: "get_destination_info", Description: "Get quick facts and top attractions for one destination.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}
	searchFlights, err := googleadk.ActivityAsTool(searchFlightsActivity, googleadk.ActivityToolOptions{
		Name: "search_flights", Description: "Find flights to a destination, optionally from an origin and on a date.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}
	searchHotels, err := googleadk.ActivityAsTool(searchHotelsActivity, googleadk.ActivityToolOptions{
		Name: "search_hotels", Description: "Find hotels at a destination, optionally under a nightly price.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}
	searchAttractions, err := googleadk.ActivityAsTool(searchAttractionsActivity, googleadk.ActivityToolOptions{
		Name: "search_attractions", Description: "List attractions and activities at a destination.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}
	bookingDetails, err := googleadk.ActivityAsTool(bookingDetailsActivity, googleadk.ActivityToolOptions{
		Name: "get_booking_details", Description: "Get the items and status for a previous booking ID.", ActivityOptions: toolOptions,
	})
	if err != nil {
		return nil, err
	}

	getBookings, err := functiontool.New[EmptyArgs, map[string]any](functiontool.Config{
		Name: "get_bookings", Description: "List the signed-in traveller's previously booked trips.",
	}, func(_ agent.Context, _ EmptyArgs) (map[string]any, error) {
		var bookings []map[string]any
		err := executeActivity(s.toolCtx, toolOptions, "get_bookings", AccountArgs{AccountKey: s.accountKey}, &bookings)
		return map[string]any{"bookings": bookings}, err
	})
	if err != nil {
		return nil, err
	}

	addItinerary, err := functiontool.New[ResolveItineraryArgs, map[string]any](functiontool.Config{
		Name: "add_to_itinerary", Description: "Add items as {kind, id} using stable IDs returned by the search tools.",
	}, func(_ agent.Context, in ResolveItineraryArgs) (map[string]any, error) {
		var resolved []ItineraryItem
		if err := executeActivity(s.toolCtx, toolOptions, "resolve_itinerary_items", in, &resolved); err != nil {
			return nil, err
		}
		existing := map[string]bool{}
		for _, item := range s.itinerary {
			existing[itemID(item)] = true
		}
		added := []map[string]any{}
		for _, item := range resolved {
			if existing[itemID(item)] {
				continue
			}
			s.itinerary = append(s.itinerary, item)
			existing[itemID(item)] = true
			added = append(added, map[string]any{"item_id": itemID(item), "title": item.Title})
		}
		return map[string]any{"added": added, "itinerary_size": len(s.itinerary), "itinerary_total": itineraryTotal(s.itinerary)}, nil
	})
	if err != nil {
		return nil, err
	}

	removeItinerary, err := functiontool.New[RemoveItineraryArgs, map[string]any](functiontool.Config{
		Name: "remove_from_itinerary", Description: "Remove itinerary entries by item_id, such as flight-12.",
	}, func(_ agent.Context, in RemoveItineraryArgs) (map[string]any, error) {
		remove := map[string]bool{}
		for _, id := range in.ItemIDs {
			remove[id] = true
		}
		before := len(s.itinerary)
		kept := make([]ItineraryItem, 0, before)
		for _, item := range s.itinerary {
			if !remove[itemID(item)] {
				kept = append(kept, item)
			}
		}
		s.itinerary = kept
		return map[string]any{"removed": before - len(kept), "itinerary_size": len(kept), "itinerary_total": itineraryTotal(kept)}, nil
	})
	if err != nil {
		return nil, err
	}

	researchDestination, err := functiontool.New[ResearchDestinationArgs, map[string]any](functiontool.Config{
		Name: "research_destination", Description: "Run a slow, deep, cited plan then parallel web-search then write pass. Use only when explicitly asked for deep research.",
	}, func(_ agent.Context, in ResearchDestinationArgs) (map[string]any, error) {
		report, err := researchDestinationTool(s, in.Query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"short_summary": report.ShortSummary, "markdown_report": report.MarkdownReport}, nil
	})
	if err != nil {
		return nil, err
	}

	createInvoice, err := functiontool.New[CreateInvoiceArgs, map[string]any](functiontool.Config{
		Name: "create_invoice", Description: "Create an invoice for one selected flight. Requires human approval.",
	}, func(tctx agent.Context, in CreateInvoiceArgs) (map[string]any, error) {
		if tctx.ToolConfirmation() == nil {
			if err := tctx.RequestConfirmation("Create this flight invoice?", nil); err != nil {
				return nil, err
			}
			return map[string]any{"status": "awaiting confirmation"}, nil
		}
		if !tctx.ToolConfirmation().Confirmed {
			return map[string]any{"status": "declined"}, nil
		}
		var result map[string]any
		err := executeActivity(s.toolCtx, toolOptions, "create_invoice_record", InvoiceRecordArgs{
			AccountKey: s.accountKey, Amount: round2(in.Amount), FlightDetails: in.FlightDetails,
		}, &result)
		return result, err
	})
	if err != nil {
		return nil, err
	}

	bookTrip, err := functiontool.New[EmptyArgs, map[string]any](functiontool.Config{
		Name: "book_trip", Description: "Book the whole durable itinerary through a compensating child Workflow. Requires human approval.",
	}, func(tctx agent.Context, _ EmptyArgs) (map[string]any, error) {
		if len(s.itinerary) == 0 {
			return map[string]any{"error": "The itinerary is empty."}, nil
		}
		if tctx.ToolConfirmation() == nil {
			if err := tctx.RequestConfirmation("Book the current itinerary?", nil); err != nil {
				return nil, err
			}
			return map[string]any{"status": "awaiting confirmation"}, nil
		}
		if !tctx.ToolConfirmation().Confirmed {
			return map[string]any{"status": "declined"}, nil
		}
		total := itineraryTotal(s.itinerary)
		summary := fmt.Sprintf("%d item(s) — $%.2f", len(s.itinerary), total)
		s.checkoutAttempt++
		childCtx := workflow.WithChildOptions(s.toolCtx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("%s-checkout-%d", s.accountKey, s.checkoutAttempt),
		})
		var checkout CheckoutResult
		err := workflow.ExecuteChildWorkflow(childCtx, "CheckoutWorkflow", CheckoutRequest{
			AccountKey: s.accountKey, Items: append([]ItineraryItem(nil), s.itinerary...), Summary: summary,
		}).Get(s.toolCtx, &checkout)
		if err != nil {
			return nil, err
		}
		if checkout.Status == "booked" {
			s.itinerary = []ItineraryItem{}
		}
		return structToMap(checkout), nil
	})
	if err != nil {
		return nil, err
	}

	return llmagent.New(llmagent.Config{
		Name: "travel_planner", Description: "A friendly travel planner with durable tools and approvals.",
		Model:       googleadk.NewModel(cfg.ADKModel, googleadk.WithModelActivityOptions(modelOptions)),
		Instruction: SystemPrompt(travellerEmail),
		Tools: []adktool.Tool{
			searchEvents, searchDestinations, destinationInfo, searchFlights, searchHotels,
			searchAttractions, getBookings, bookingDetails, addItinerary, removeItinerary,
			researchDestination, createInvoice, bookTrip,
		},
	})
}

func installHandlers(ctx workflow.Context, s *agentState, r *runner.Runner) error {
	if err := workflow.SetUpdateHandler(ctx, "send_message", func(updateCtx workflow.Context, text string) (TurnResult, error) {
		if s.turnInProgress {
			return TurnResult{}, temporal.NewNonRetryableApplicationError("A turn is already in progress.", "TurnInProgress", nil)
		}
		if strings.TrimSpace(text) == "" {
			return TurnResult{}, temporal.NewNonRetryableApplicationError("Message must not be empty.", "InvalidMessage", nil)
		}
		s.turnInProgress = true
		s.approval = nil
		s.messages = append(s.messages, ChatMessage{Role: "user", Content: text})
		if err := workflow.Await(updateCtx, func() bool { return !s.llmDown }); err != nil {
			return TurnResult{}, err
		}
		s.toolCtx = updateCtx
		answer, pending, err := runADKPass(updateCtx, r, s.accountKey, genai.NewContentFromText(text, genai.RoleUser))
		if err != nil {
			answer = "I couldn't complete that turn: " + err.Error()
			finishTurn(s, answer)
			return TurnResult{Status: "reply", Reply: answer}, nil
		}
		if pending != nil {
			s.pendingConfirmation = confirmationView(s, *pending)
			workflow.Go(updateCtx, func(resumeCtx workflow.Context) {
				resumeAfterApproval(resumeCtx, s, r, pending.FunctionCallID)
			})
			return TurnResult{Status: "awaiting_approval", Reply: answer}, nil
		}
		finishTurn(s, answer)
		return TurnResult{Status: "reply", Reply: answer}, nil
	}); err != nil {
		return err
	}

	confirm := workflow.GetSignalChannel(ctx, "confirm_action")
	workflow.Go(ctx, func(signalCtx workflow.Context) {
		for {
			var decision ApprovalDecision
			confirm.Receive(signalCtx, &decision)
			s.approval = &decision
		}
	})
	llmStatus := workflow.GetSignalChannel(ctx, "set_llm_status")
	workflow.Go(ctx, func(signalCtx workflow.Context) {
		for {
			llmStatus.Receive(signalCtx, &s.llmDown)
		}
	})

	queries := []struct {
		name    string
		handler any
	}{
		{"is_llm_down", func() bool { return s.llmDown }},
		{"transcript", func() []ChatMessage { return s.messages }},
		{"pending_approval", func() *PendingConfirmation { return s.pendingConfirmation }},
		{"research_status", func() ResearchStatus {
			return ResearchStatus{Phase: s.phase, Plan: s.plan, SearchesTotal: s.searchesTotal, SearchesDone: s.searchesDone}
		}},
		{"itinerary_view", func() []ItineraryItem { return s.itinerary }},
	}
	for _, query := range queries {
		if err := workflow.SetQueryHandler(ctx, query.name, query.handler); err != nil {
			return err
		}
	}
	return nil
}

func runADKPass(ctx workflow.Context, r *runner.Runner, userID string, message *genai.Content) (string, *googleadk.PendingConfirmation, error) {
	events := []*session.Event{}
	answer := ""
	for event, err := range r.Run(
		googleadk.NewContext(ctx, googleadk.WithSequentialToolFanout()),
		userID, "travel-session", message, agent.RunConfig{},
	) {
		if err != nil {
			return "", nil, err
		}
		if event == nil {
			continue
		}
		events = append(events, event)
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				if part != nil && part.Text != "" {
					answer = part.Text
				}
			}
		}
	}
	pending := googleadk.PendingConfirmations(events)
	if len(pending) > 0 {
		return answer, &pending[0], nil
	}
	return answer, nil, nil
}

func resumeAfterApproval(ctx workflow.Context, s *agentState, r *runner.Runner, confirmationID string) {
	if err := workflow.Await(ctx, func() bool { return s.approval != nil }); err != nil {
		finishTurn(s, "I couldn't wait for the approval: "+err.Error())
		return
	}
	decision := *s.approval
	s.approval = nil
	s.pendingConfirmation = nil
	s.toolCtx = ctx
	answer, pending, err := runADKPass(ctx, r, s.accountKey, googleadk.ConfirmationResponse(googleadk.ConfirmationDecision{
		FunctionCallID: confirmationID, Confirmed: decision.Approved,
	}))
	if err != nil {
		finishTurn(s, "I couldn't finish the approved action: "+err.Error())
		return
	}
	if pending != nil {
		s.pendingConfirmation = confirmationView(s, *pending)
		workflow.Go(ctx, func(nextCtx workflow.Context) { resumeAfterApproval(nextCtx, s, r, pending.FunctionCallID) })
		return
	}
	finishTurn(s, answer)
}

func confirmationView(s *agentState, pending googleadk.PendingConfirmation) *PendingConfirmation {
	if pending.OriginalCall != nil && pending.OriginalCall.Name == "create_invoice" {
		amount := round2(floatValue(pending.OriginalCall.Args["amount"]))
		details := stringValue(pending.OriginalCall.Args["flight_details"])
		return &PendingConfirmation{Action: "create_invoice", Title: "Create invoice", Detail: details, Amount: amount, Args: map[string]any{"amount": amount, "flight_details": details}}
	}
	total := itineraryTotal(s.itinerary)
	items := make([]map[string]any, 0, len(s.itinerary))
	for _, item := range s.itinerary {
		items = append(items, map[string]any{"title": item.Title, "price": item.Price})
	}
	return &PendingConfirmation{Action: "book_trip", Title: "Booking approval required", Detail: fmt.Sprintf("%d item(s) — $%.2f", len(s.itinerary), total), Amount: total, Args: map[string]any{"items": items}}
}

func researchDestinationTool(s *agentState, query string) (ReportData, error) {
	s.plan, s.searchesTotal, s.searchesDone, s.phase = []SearchItem{}, 0, 0, "planning"
	researchRetry := &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, NonRetryableErrorTypes: []string{"LLMFatalError"}}
	planOptions := workflow.ActivityOptions{StartToCloseTimeout: 90 * time.Second, RetryPolicy: researchRetry}
	var plan SearchPlan
	if err := executeActivity(s.toolCtx, planOptions, "plan_searches", query, &plan); err != nil {
		return ReportData{}, err
	}
	s.plan, s.searchesTotal, s.phase = plan.Searches, len(plan.Searches), "searching"
	type searchResult struct {
		index int
		text  string
		err   error
	}
	results := workflow.NewChannel(s.toolCtx)
	for i, item := range plan.Searches {
		i, item := i, item
		workflow.Go(s.toolCtx, func(searchCtx workflow.Context) {
			var text string
			err := executeActivity(searchCtx, workflow.ActivityOptions{StartToCloseTimeout: 120 * time.Second, RetryPolicy: researchRetry}, "web_search", item, &text)
			results.Send(searchCtx, searchResult{index: i, text: text, err: err})
		})
	}
	findings := make([]string, len(plan.Searches))
	for range plan.Searches {
		var result searchResult
		results.Receive(s.toolCtx, &result)
		if result.err != nil {
			return ReportData{}, result.err
		}
		findings[result.index] = result.text
		s.searchesDone++
	}
	s.phase = "writing"
	var report ReportData
	err := executeActivity(s.toolCtx, workflow.ActivityOptions{StartToCloseTimeout: 180 * time.Second, RetryPolicy: researchRetry}, "write_report", WriteRequest{Brief: query, Findings: findings}, &report)
	s.phase = "idle"
	return report, err
}

func executeActivity(ctx workflow.Context, options workflow.ActivityOptions, name string, input any, output any) error {
	activityCtx := workflow.WithActivityOptions(ctx, options)
	return workflow.ExecuteActivity(activityCtx, name, input).Get(ctx, output)
}

func finishTurn(s *agentState, answer string) {
	if answer != "" {
		s.messages = append(s.messages, ChatMessage{Role: "assistant", Content: answer})
	}
	s.phase = "idle"
	s.pendingConfirmation = nil
	s.turnInProgress = false
}

func itemID(item ItineraryItem) string { return fmt.Sprintf("%s-%d", item.Kind, item.RefID) }
func itineraryTotal(items []ItineraryItem) float64 {
	total := 0.0
	for _, item := range items {
		total += item.Price
	}
	return round2(total)
}

func structToMap(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	decoded := map[string]any{}
	_ = json.Unmarshal(encoded, &decoded)
	return decoded
}

// CheckoutWorkflow makes the booking order and reverse compensation explicit.
func CheckoutWorkflow(ctx workflow.Context, req CheckoutRequest) (CheckoutResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 5 * time.Second, MaximumAttempts: 3, NonRetryableErrorTypes: []string{"HotelBookingFailed", "BookingDeclined"}},
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	reservations := []CheckoutReservation{}
	activityByKind := map[string]string{"flight": "book_flight", "hotel": "book_hotel", "activity": "book_activity"}
	for _, kind := range []string{"flight", "hotel", "activity"} {
		for _, item := range req.Items {
			if item.Kind != kind {
				continue
			}
			var reservation CheckoutReservation
			err := workflow.ExecuteActivity(ctx, activityByKind[kind], CheckoutStepRequest{AccountKey: req.AccountKey, Item: item}).Get(ctx, &reservation)
			if err != nil {
				return compensateCheckout(ctx, reservations, err), nil
			}
			reservations = append(reservations, reservation)
		}
	}
	var finalized FinalizeCheckoutResult
	if err := workflow.ExecuteActivity(ctx, "finalize_checkout", req).Get(ctx, &finalized); err != nil {
		return compensateCheckout(ctx, reservations, err), nil
	}
	bookingID := finalized.BookingID
	return CheckoutResult{Status: "booked", Message: "Checkout completed and every itinerary item is booked.", WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID, Reservations: reservations, Compensations: []CheckoutReservation{}, BookingID: &bookingID}, nil
}

func compensateCheckout(ctx workflow.Context, reservations []CheckoutReservation, cause error) CheckoutResult {
	compensations := []CheckoutReservation{}
	cancelByKind := map[string]string{"flight": "cancel_flight", "hotel": "cancel_hotel", "activity": "cancel_activity"}
	for i := len(reservations) - 1; i >= 0; i-- {
		var cancelled CheckoutReservation
		if workflow.ExecuteActivity(ctx, cancelByKind[reservations[i].Kind], reservations[i]).Get(ctx, &cancelled) == nil {
			compensations = append(compensations, cancelled)
		}
	}
	names := []string{}
	for _, item := range compensations {
		names = append(names, item.Title)
	}
	if len(names) == 0 {
		names = []string{"no prior reservations"}
	}
	failure := failureMessage(cause)
	return CheckoutResult{Status: "compensated", Message: "Checkout stopped: " + failure + " Compensation completed: cancelled " + strings.Join(names, ", ") + ".", WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID, Reservations: reservations, Compensations: compensations, Failure: &failure}
}

func failureMessage(err error) string {
	if err == nil {
		return "That action could not be completed."
	}
	return err.Error()
}
