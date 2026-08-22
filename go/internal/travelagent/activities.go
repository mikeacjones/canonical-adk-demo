package travelagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// Activities owns every side effect used by the workflows: Gemini HTTP,
// Postgres, and the checkout provider stand-ins. Workflow code stays pure.
type Activities struct {
	cfg      Config
	db       *DB
	temporal client.Client
	http     *http.Client
}

func NewActivities(cfg Config, db *DB, temporalClient client.Client) *Activities {
	return &Activities{cfg: cfg, db: db, temporal: temporalClient, http: &http.Client{Timeout: 55 * time.Second}}
}

func (a *Activities) llmDown(ctx context.Context) bool {
	info := activity.GetInfo(ctx)
	if info.WorkflowExecution.ID == "" {
		return false
	}
	value, err := a.temporal.QueryWorkflow(ctx, info.WorkflowExecution.ID, "", "is_llm_down")
	if err != nil {
		return false
	}
	var down bool
	return value.Get(&down) == nil && down
}

func (a *Activities) BookFlight(ctx context.Context, req CheckoutStepRequest) (CheckoutReservation, error) {
	return a.reserve(ctx, req)
}

func (a *Activities) BookHotel(ctx context.Context, req CheckoutStepRequest) (CheckoutReservation, error) {
	if a.cfg.CheckoutStepDelay > 0 {
		time.Sleep(a.cfg.CheckoutStepDelay)
	}
	if a.cfg.CheckoutFailHotel {
		return CheckoutReservation{}, temporal.NewNonRetryableApplicationError("Hotel booking failed — the supplier returned no availability (injected demo failure).", "HotelBookingFailed", nil)
	}
	return reservation(req), nil
}

func (a *Activities) BookActivity(ctx context.Context, req CheckoutStepRequest) (CheckoutReservation, error) {
	return a.reserve(ctx, req)
}

func (a *Activities) reserve(_ context.Context, req CheckoutStepRequest) (CheckoutReservation, error) {
	if a.cfg.CheckoutStepDelay > 0 {
		time.Sleep(a.cfg.CheckoutStepDelay)
	}
	return reservation(req), nil
}

func reservation(req CheckoutStepRequest) CheckoutReservation {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", req.AccountKey, req.Item.Kind, req.Item.RefID)))
	prefix := map[string]string{"flight": "FLT", "hotel": "HTL", "activity": "ACT"}[req.Item.Kind]
	return CheckoutReservation{Kind: req.Item.Kind, RefID: req.Item.RefID, Title: req.Item.Title, ReservationID: prefix + "-" + strings.ToUpper(hex.EncodeToString(digest[:])[:10]), Status: "booked"}
}

func (a *Activities) CancelFlight(ctx context.Context, res CheckoutReservation) (CheckoutReservation, error) {
	return a.cancel(ctx, res)
}
func (a *Activities) CancelHotel(ctx context.Context, res CheckoutReservation) (CheckoutReservation, error) {
	return a.cancel(ctx, res)
}
func (a *Activities) CancelActivity(ctx context.Context, res CheckoutReservation) (CheckoutReservation, error) {
	return a.cancel(ctx, res)
}
func (a *Activities) cancel(_ context.Context, res CheckoutReservation) (CheckoutReservation, error) {
	if a.cfg.CheckoutStepDelay > 0 {
		time.Sleep(a.cfg.CheckoutStepDelay)
	}
	res.Status = "cancelled"
	return res, nil
}

func (a *Activities) FinalizeCheckout(ctx context.Context, req CheckoutRequest) (FinalizeCheckoutResult, error) {
	already, err := a.db.ItemsAlreadyBooked(ctx, req.AccountKey, req.Items)
	if err != nil {
		return FinalizeCheckoutResult{}, err
	}
	if len(already) > 0 {
		names := make([]string, 0, len(already))
		for _, item := range already {
			names = append(names, fmt.Sprintf("%q", stringValue(item["title"])))
		}
		return FinalizeCheckoutResult{}, temporal.NewNonRetryableApplicationError("Booking declined — this trip already includes "+strings.Join(names, ", ")+".", "BookingDeclined", nil)
	}
	booking, err := a.db.RecordBooking(ctx, req.AccountKey, req.Items, req.Summary)
	if err != nil {
		return FinalizeCheckoutResult{}, err
	}
	return FinalizeCheckoutResult{BookingID: int64Value(booking["booking_id"])}, nil
}
