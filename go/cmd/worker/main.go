package main

import (
	"context"
	"log"

	"github.com/temporal-sa/canonical-adk-demo/go/internal/travelagent"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/contrib/googleadk"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
)

func main() {
	cfg := travelagent.LoadConfig()
	temporalClient, err := travelagent.DialTemporal(cfg)
	if err != nil {
		log.Fatalf("connect to Temporal: %v", err)
	}
	defer temporalClient.Close()

	db, err := travelagent.NewDB(context.Background(), cfg.DBURL)
	if err != nil {
		log.Fatalf("configure Postgres: %v", err)
	}
	defer db.Close()

	adkPlugin, err := googleadk.NewPlugin(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			cfg.ADKModel: func(ctx context.Context, name string) (model.LLM, error) {
				return gemini.NewModel(ctx, name, nil)
			},
		},
	})
	if err != nil {
		log.Fatalf("configure Google ADK plugin: %v", err)
	}

	activities := travelagent.NewActivities(cfg, db, temporalClient)
	w := worker.New(temporalClient, cfg.TaskQueue, worker.Options{
		Plugins: []worker.Plugin{adkPlugin},
	})
	w.RegisterWorkflowWithOptions(travelagent.TravelAgentWorkflow, workflow.RegisterOptions{Name: "TravelAgentWorkflow"})
	w.RegisterWorkflowWithOptions(travelagent.CheckoutWorkflow, workflow.RegisterOptions{Name: "CheckoutWorkflow"})

	w.RegisterActivityWithOptions(activities.SearchEvents, activity.RegisterOptions{Name: "search_events"})
	w.RegisterActivityWithOptions(activities.SearchDestinations, activity.RegisterOptions{Name: "search_destinations"})
	w.RegisterActivityWithOptions(activities.GetDestinationInfo, activity.RegisterOptions{Name: "get_destination_info"})
	w.RegisterActivityWithOptions(activities.SearchFlights, activity.RegisterOptions{Name: "search_flights"})
	w.RegisterActivityWithOptions(activities.SearchHotels, activity.RegisterOptions{Name: "search_hotels"})
	w.RegisterActivityWithOptions(activities.SearchAttractions, activity.RegisterOptions{Name: "search_attractions"})
	w.RegisterActivityWithOptions(activities.GetBookings, activity.RegisterOptions{Name: "get_bookings"})
	w.RegisterActivityWithOptions(activities.GetBookingDetails, activity.RegisterOptions{Name: "get_booking_details"})
	w.RegisterActivityWithOptions(activities.ResolveItineraryItems, activity.RegisterOptions{Name: "resolve_itinerary_items"})
	w.RegisterActivityWithOptions(activities.CreateInvoiceRecord, activity.RegisterOptions{Name: "create_invoice_record"})
	w.RegisterActivityWithOptions(activities.PlanSearchesGemini, activity.RegisterOptions{Name: "plan_searches"})
	w.RegisterActivityWithOptions(activities.WebSearchGemini, activity.RegisterOptions{Name: "web_search"})
	w.RegisterActivityWithOptions(activities.WriteReportGemini, activity.RegisterOptions{Name: "write_report"})
	w.RegisterActivityWithOptions(activities.BookFlight, activity.RegisterOptions{Name: "book_flight"})
	w.RegisterActivityWithOptions(activities.BookHotel, activity.RegisterOptions{Name: "book_hotel"})
	w.RegisterActivityWithOptions(activities.BookActivity, activity.RegisterOptions{Name: "book_activity"})
	w.RegisterActivityWithOptions(activities.CancelFlight, activity.RegisterOptions{Name: "cancel_flight"})
	w.RegisterActivityWithOptions(activities.CancelHotel, activity.RegisterOptions{Name: "cancel_hotel"})
	w.RegisterActivityWithOptions(activities.CancelActivity, activity.RegisterOptions{Name: "cancel_activity"})
	w.RegisterActivityWithOptions(activities.FinalizeCheckout, activity.RegisterOptions{Name: "finalize_checkout"})

	log.Printf("go Google ADK worker polling %q on %s (namespace: %s, model: %s)", cfg.TaskQueue, cfg.TemporalAddress, cfg.TemporalNamespace, cfg.ADKModel)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}
