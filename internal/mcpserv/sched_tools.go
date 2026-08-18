package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/schedule"
)

// Scheduler bundles what the schedule tools need: the schedule-time gate
// (whose deliverer stores entries instead of sending — see
// schedule.StoreDeliverer) and the schedule store itself.
type Scheduler struct {
	Gate  *gate.Gate
	Store *schedule.Store
}

// schedDeps holds the schedule tool handlers' dependencies, mirroring
// sendDeps so tests can call handlers without a transport.
type schedDeps struct {
	st    Store
	g     *gate.Gate
	sched *schedule.Store
}

// registerScheduleTools registers schedule_send, list_scheduled, and
// cancel_scheduled.
func registerScheduleTools(server *mcp.Server, st Store, sched *Scheduler) {
	d := &schedDeps{st: st, g: sched.Gate, sched: sched.Store}

	mcp.AddTool(server, &mcp.Tool{
		Name: "schedule_send",
		Description: "Schedules a message (or media file) to send at a future time, up to 30 days " +
			"ahead. Give the fire time as `send_at` (RFC 3339 or Unix seconds) or `delay_minutes` " +
			"— exactly one. The same gate as live sends applies AT SCHEDULING TIME: an untrusted " +
			"recipient returns a preview (fire time included) and a draft_token, and only " +
			"re-issuing the identical call with that token stores the schedule; a trusted " +
			"recipient schedules on the first call. At fire time the send consumes the shared " +
			"send rate limiter like any other send. Schedules persist across server restarts, " +
			"but only fire while the server is RUNNING — one that comes due while the server is " +
			"down fires on the next start only if it is less than 15 minutes late, and is " +
			"dropped otherwise. Cancel with cancel_scheduled.",
	}, d.scheduleSend)

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_scheduled",
		Description: "Lists pending scheduled sends, soonest first, one per line: id, fire time " +
			"(RFC 3339 UTC), description. These are this server's own pending sends, not WhatsApp " +
			"content — no untrusted-data banner applies.",
	}, d.listScheduled)

	mcp.AddTool(server, &mcp.Tool{
		Name: "cancel_scheduled",
		Description: "Cancels one pending scheduled send by its id (from schedule_send or " +
			"list_scheduled). Cancelling is always allowed — it only ever prevents a send.",
	}, d.cancelScheduled)
}

type scheduleSendInput struct {
	To           string `json:"to" jsonschema:"Recipient JID (contact or group) to send to."`
	Text         string `json:"text,omitempty" jsonschema:"Message text, or the caption when path is set."`
	Path         string `json:"path,omitempty" jsonschema:"Local filesystem path of a media file to send instead of a text message; must stay readable until the fire time."`
	SendAt       string `json:"send_at,omitempty" jsonschema:"When to send: RFC 3339 timestamp or Unix seconds. Use exactly one of send_at and delay_minutes."`
	DelayMinutes int    `json:"delay_minutes,omitempty" jsonschema:"Send this many minutes from now. Use exactly one of send_at and delay_minutes."`
	DraftToken   string `json:"draft_token,omitempty" jsonschema:"Token returned by a prior unsent draft of this exact call; supply it to commit the schedule."`
}

// resolveFireAt turns the exactly-one-of send_at/delay_minutes pair into a
// Unix fire time. Past/horizon checks live in schedule.StoreDeliverer's
// Validate, where they also cover the commit call.
func resolveFireAt(sendAt string, delayMinutes int, now time.Time) (int64, error) {
	switch {
	case sendAt != "" && delayMinutes != 0:
		return 0, errors.New("use exactly one of send_at and delay_minutes")
	case sendAt == "" && delayMinutes == 0:
		return 0, errors.New("give a fire time: send_at (RFC 3339 or unix seconds) or delay_minutes")
	case delayMinutes != 0:
		if delayMinutes < 0 {
			return 0, errors.New("delay_minutes must be positive")
		}
		return now.Add(time.Duration(delayMinutes) * time.Minute).Unix(), nil
	}

	if unix, err := strconv.ParseInt(sendAt, 10, 64); err == nil {
		return unix, nil
	}
	t, err := time.Parse(time.RFC3339, sendAt)
	if err != nil {
		return 0, errors.New("send_at is not a recognized time: use RFC 3339 (e.g. 2026-08-19T08:00:00+05:30) or Unix seconds")
	}
	return t.Unix(), nil
}

func (d *schedDeps) scheduleSend(ctx context.Context, _ *mcp.CallToolRequest, in scheduleSendInput) (*mcp.CallToolResult, any, error) {
	fireAt, err := resolveFireAt(in.SendAt, in.DelayMinutes, time.Now())
	if err != nil {
		return nil, nil, err
	}

	kind := "text"
	if in.Path != "" {
		if err := validateLocalFile(in.Path, "media file"); err != nil {
			return nil, nil, err
		}
		kind = "media"
	} else if strings.TrimSpace(in.Text) == "" {
		return nil, nil, errors.New("text is required for a text message")
	}

	delivery := gate.Delivery{Kind: kind, To: in.To, Text: in.Text, Path: in.Path, FireAt: fireAt}
	res, err := d.g.Submit(ctx, delivery, in.DraftToken, resolveContactName(d.st))
	if err != nil {
		return nil, nil, err
	}
	return textResult(renderScheduleResult(res)), nil, nil
}

// renderScheduleResult mirrors renderSendResult, with the committed line
// carrying the schedule id rather than a message id.
func renderScheduleResult(res gate.Result) string {
	banner := Banner(res.Preview)
	if !res.Sent {
		return banner + "\ndraft_token: " + res.DraftToken +
			"\nConfirm with the user, then re-issue this call with draft_token to schedule."
	}
	return banner + "\nscheduled_id: " + res.MessageID +
		"\nDelivers only while the server is running at the fire time; cancel with cancel_scheduled."
}

func (d *schedDeps) listScheduled(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	entries := d.sched.List()
	if len(entries) == 0 {
		return textResult("no scheduled sends"), nil, nil
	}
	rows := make([]string, len(entries))
	for i, e := range entries {
		rows[i] = fmt.Sprintf("%s\t%s\t%s", e.ID, time.Unix(e.Delivery.FireAt, 0).UTC().Format(time.RFC3339), e.Preview)
	}
	return textResult(strings.Join(rows, "\n")), nil, nil
}

type cancelScheduledInput struct {
	ID string `json:"id" jsonschema:"Id of the pending schedule to cancel, as returned by schedule_send or list_scheduled."`
}

func (d *schedDeps) cancelScheduled(_ context.Context, _ *mcp.CallToolRequest, in cancelScheduledInput) (*mcp.CallToolResult, any, error) {
	removed, err := d.sched.Remove(in.ID)
	if err != nil {
		return nil, nil, err
	}
	if !removed {
		return nil, nil, errors.New("no pending schedule with that id — it may have fired, been cancelled, or never existed")
	}
	return textResult("cancelled: " + in.ID), nil, nil
}
