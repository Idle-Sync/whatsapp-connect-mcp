package mcpserv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/schedule"
)

// newSchedDeps wires the schedule tools the way serve does: a real
// schedule.Store in a temp dir behind a real gate whose deliverer stores
// entries instead of sending.
func newSchedDeps(t *testing.T) (*schedDeps, *schedule.Store) {
	t.Helper()
	store, dropped, err := schedule.Load(t.TempDir(), time.Now())
	if err != nil || len(dropped) != 0 {
		t.Fatalf("schedule.Load = (dropped %v, %v)", dropped, err)
	}
	deliverer := schedule.NewStoreDeliverer(store, func(gate.Delivery) error { return nil }, time.Now)
	g := gate.New(deliverer, func(string) bool { return false }, 3, 5, time.Now)
	return &schedDeps{st: &sendFakeStore{}, g: g, sched: store}, store
}

func TestScheduleSendTwoStepCommitsIntoTheSchedule(t *testing.T) {
	d, store := newSchedDeps(t)
	sendAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	in := scheduleSendInput{To: "111@s.whatsapp.net", Text: "morning!", SendAt: sendAt}
	first, _, err := d.scheduleSend(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("scheduleSend(draft) error = %v", err)
	}
	text := resultText(t, first)
	token := extractField(t, text, "draft_token")
	if token == "" {
		t.Fatal("draft result missing draft_token")
	}
	if !strings.Contains(text, "send_at=") {
		t.Fatalf("draft preview = %q, want it to show the fire time the human is confirming", text)
	}
	if len(store.List()) != 0 {
		t.Fatal("draft alone stored a schedule; nothing may be stored before the confirm")
	}

	in.DraftToken = token
	second, _, err := d.scheduleSend(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("scheduleSend(commit) error = %v", err)
	}
	text2 := resultText(t, second)
	id := extractField(t, text2, "scheduled_id")
	if id == "" {
		t.Fatal("commit result missing scheduled_id")
	}

	list := store.List()
	if len(list) != 1 || list[0].ID != id || list[0].Delivery.Text != "morning!" {
		t.Fatalf("stored schedules = %+v, want the committed entry", list)
	}
}

func TestScheduleSendRejectsPastTime(t *testing.T) {
	d, _ := newSchedDeps(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	_, _, err := d.scheduleSend(context.Background(), nil, scheduleSendInput{
		To: "111@s.whatsapp.net", Text: "late", SendAt: past,
	})
	if err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("error = %v, want a must-be-in-the-future refusal on the first call", err)
	}
}

func TestScheduleSendRequiresExactlyOneTimeForm(t *testing.T) {
	d, _ := newSchedDeps(t)
	sendAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	for name, in := range map[string]scheduleSendInput{
		"neither": {To: "1@s.whatsapp.net", Text: "x"},
		"both":    {To: "1@s.whatsapp.net", Text: "x", SendAt: sendAt, DelayMinutes: 5},
	} {
		if _, _, err := d.scheduleSend(context.Background(), nil, in); err == nil {
			t.Errorf("scheduleSend(%s): want an error, got nil", name)
		}
	}
}

func TestScheduleSendDelayMinutesForm(t *testing.T) {
	d, store := newSchedDeps(t)

	first, _, err := d.scheduleSend(context.Background(), nil, scheduleSendInput{
		To: "111@s.whatsapp.net", Text: "soon", DelayMinutes: 30,
	})
	if err != nil {
		t.Fatalf("scheduleSend(draft) error = %v", err)
	}
	token := extractField(t, resultText(t, first), "draft_token")
	if _, _, err := d.scheduleSend(context.Background(), nil, scheduleSendInput{
		To: "111@s.whatsapp.net", Text: "soon", DelayMinutes: 30, DraftToken: token,
	}); err != nil {
		t.Fatalf("scheduleSend(commit) error = %v", err)
	}

	list := store.List()
	if len(list) != 1 {
		t.Fatalf("stored schedules = %+v, want one", list)
	}
	wantAt := time.Now().Add(30 * time.Minute).Unix()
	if diff := list[0].Delivery.FireAt - wantAt; diff < -60 || diff > 60 {
		t.Fatalf("FireAt = %d, want about %d", list[0].Delivery.FireAt, wantAt)
	}
}

func TestListScheduledShowsPendingAndCancelRemoves(t *testing.T) {
	d, store := newSchedDeps(t)

	entry, err := store.Add(gate.Delivery{
		Kind: "text", To: "111@s.whatsapp.net", Text: "hello", FireAt: time.Now().Add(time.Hour).Unix(),
	}, "text to 111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	listRes, _, err := d.listScheduled(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("listScheduled error = %v", err)
	}
	if !strings.Contains(resultText(t, listRes), entry.ID) {
		t.Fatalf("list = %q, want it to name the pending schedule", resultText(t, listRes))
	}

	cancelRes, _, err := d.cancelScheduled(context.Background(), nil, cancelScheduledInput{ID: entry.ID})
	if err != nil {
		t.Fatalf("cancelScheduled error = %v", err)
	}
	if !strings.Contains(resultText(t, cancelRes), "cancelled") {
		t.Fatalf("cancel result = %q, want a cancellation confirmation", resultText(t, cancelRes))
	}
	if len(store.List()) != 0 {
		t.Fatal("entry still present after cancel")
	}

	if _, _, err := d.cancelScheduled(context.Background(), nil, cancelScheduledInput{ID: entry.ID}); err == nil {
		t.Fatal("cancelling an unknown id succeeded, want an error")
	}
}

func TestListScheduledEmpty(t *testing.T) {
	d, _ := newSchedDeps(t)
	res, _, err := d.listScheduled(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("listScheduled error = %v", err)
	}
	if !strings.Contains(resultText(t, res), "no scheduled sends") {
		t.Fatalf("empty list = %q, want a no-scheduled-sends notice", resultText(t, res))
	}
}
