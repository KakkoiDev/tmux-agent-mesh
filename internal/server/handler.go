// Package server implements the mesh protocol (docs/protocol.md) over unix
// socket and stdio transports, backed by the store.
package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KakkoiDev/tmux-agent-mesh/internal/store"
)

// Request is a single JSON Lines request.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Auth   Auth            `json:"auth"`
}

// Auth identifies the caller.
type Auth struct {
	SessionID string `json:"session_id"`
	Host      string `json:"host"`
}

// Response is a single JSON Lines response.
type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Error is a machine-readable error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Handler dispatches JSON Lines requests to store methods.
type Handler struct {
	Store *store.Store
	Caps  store.Caps
}

// Handle processes a single request and returns a response.
func (h *Handler) Handle(req Request) Response {
	switch req.Method {

	// ── registry ──────────────────────────────────────────────────

	case "roster":
		return h.handleRoster(req)
	case "agent":
		return h.handleAgent(req)
	case "ping":
		return h.handlePing(req)
	case "status":
		return h.handleStatus(req)

	// ── messaging ─────────────────────────────────────────────────

	case "pending":
		return h.handlePending(req)
	case "claim":
		return h.handleClaim(req)
	case "history":
		return h.handleHistory(req)
	case "mark_read":
		return h.handleMarkRead(req)
	case "receipts":
		return h.handleReceipts(req)
	case "search":
		return h.handleSearch(req)

	// ── channels ──────────────────────────────────────────────────

	case "channel_list":
		return h.handleChannelList(req)
	case "channel_create":
		return h.handleChannelCreate(req)
	case "channel_join":
		return h.handleChannelJoin(req)
	case "channel_leave":
		return h.handleChannelLeave(req)
	case "channel_rule_add":
		return h.handleChannelRuleAdd(req)
	case "channel_rule_list":
		return h.handleChannelRuleList(req)
	case "dm":
		return h.handleDM(req)

	// ── dispatch ──────────────────────────────────────────────────

	case "dispatch":
		return h.handleDispatch(req)

	default:
		return errResponse(req.ID, "invalid", fmt.Sprintf("unknown method %q", req.Method))
	}
}

var blank = json.RawMessage("{}")

func (h *Handler) handleRoster(req Request) Response {
	agents, err := h.Store.Roster()
	if err != nil {
		return errResponse(req.ID, "internal", err.Error())
	}
	type RosterEntry struct {
		SessionID   string `json:"session_id"`
		Harness     string `json:"harness"`
		Alias       string `json:"alias"`
		Host        string `json:"host"`
		Project     string `json:"project"`
		State       string `json:"state"`
		PushCapable bool   `json:"push_capable"`
		Pending     int    `json:"pending"`
		Pane        string `json:"pane"`
		Model       string `json:"model"`
	}
	result := make([]RosterEntry, 0, len(agents))
	for _, a := range agents {
		result = append(result, RosterEntry{
			SessionID:   a.SessionID,
			Harness:     a.Harness,
			Alias:       a.Alias,
			Host:        a.Host,
			Project:     a.Project,
			State:       a.TurnState,
			PushCapable: a.PushCapable,
			Pending:     a.Pending,
			Pane:        a.TmuxTarget,
			Model:       a.Model,
		})
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleAgent(req Request) Response {
	var p struct {
		Ref string `json:"ref"`
	}
	json.Unmarshal(req.Params, &p)
	sid, err := h.resolveRef(p.Ref, req.Auth.SessionID)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	a, err := h.Store.Agent(sid)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	return okResponse(req.ID, map[string]any{
		"session_id":      a.SessionID,
		"harness":         a.Harness,
		"alias":           a.Alias,
		"model":           a.Model,
		"host":            a.Host,
		"project":         a.Project,
		"state":           a.TurnState,
		"push_capable":    a.PushCapable,
		"pending":         a.Pending,
		"pane":            a.TmuxTarget,
		"block_streak":    a.BlockStreak,
		"cwd":             a.Cwd,
		"transcript_path": a.TranscriptPath,
		"last_seen":       a.LastSeen,
	})
}

func (h *Handler) handlePing(req Request) Response {
	var p struct {
		Ref string `json:"ref"`
	}
	json.Unmarshal(req.Params, &p)
	sid, err := h.resolveRef(p.Ref, req.Auth.SessionID)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	a, err := h.Store.Agent(sid)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	return okResponse(req.ID, map[string]any{
		"session_id": a.SessionID,
		"state":      a.TurnState,
		"last_seen":  a.LastSeen,
		"pending":    a.Pending,
		"model":      a.Model,
	})
}

func (h *Handler) handleStatus(req Request) Response {
	var p struct {
		State  string `json:"state"`
		Detail string `json:"detail"`
	}
	json.Unmarshal(req.Params, &p)
	if err := h.Store.SetTurnState(req.Auth.SessionID, p.State); err != nil {
		return errResponse(req.ID, "invalid", err.Error())
	}
	return okResponse(req.ID, struct{}{})
}

func (h *Handler) handlePending(req Request) Response {
	msgs, err := h.Store.Pending(req.Auth.SessionID)
	if err != nil {
		return errResponse(req.ID, "internal", err.Error())
	}
	result := make([]MessageJSON, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, messageToJSON(m))
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleClaim(req Request) Response {
	var p struct {
		Via string `json:"via"`
	}
	json.Unmarshal(req.Params, &p)
	msgs, err := h.Store.Claim(req.Auth.SessionID, p.Via)
	if err != nil {
		return errResponse(req.ID, "invalid", err.Error())
	}
	result := make([]MessageJSON, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, messageToJSON(m))
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleHistory(req Request) Response {
	var p struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
	}
	json.Unmarshal(req.Params, &p)
	if p.Channel == "" {
		return errResponse(req.ID, "invalid", "channel is required")
	}
	ch, err := h.Store.ChannelByName(p.Channel)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	msgs, err := h.Store.History(ch.ID, req.Auth.SessionID, p.Limit)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	result := make([]MessageJSON, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, messageToJSON(m))
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleMarkRead(req Request) Response {
	var p struct {
		MessageIDs []int64 `json:"message_ids"`
	}
	json.Unmarshal(req.Params, &p)
	count := 0
	for _, mid := range p.MessageIDs {
		if err := h.Store.MarkRead(mid, req.Auth.SessionID); err == nil {
			count++
		}
	}
	return okResponse(req.ID, map[string]int{"count": count})
}

func (h *Handler) handleReceipts(req Request) Response {
	var p struct {
		MessageID int64 `json:"message_id"`
	}
	json.Unmarshal(req.Params, &p)
	receipts, err := h.Store.Receipts(p.MessageID, req.Auth.SessionID)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	type ReceiptJSON struct {
		Reader  string `json:"reader"`
		Name    string `json:"name"`
		Harness string `json:"harness"`
		At      int64  `json:"at"`
		Source  string `json:"source"`
	}
	result := make([]ReceiptJSON, 0, len(receipts))
	for _, r := range receipts {
		result = append(result, ReceiptJSON{
			Reader: r.Reader, Name: r.Name, Harness: r.Harness,
			At: r.At, Source: r.Source,
		})
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleSearch(req Request) Response {
	return errResponse(req.ID, "invalid", "search not yet implemented")
}

func (h *Handler) handleChannelList(req Request) Response {
	channels, err := h.Store.Channels()
	if err != nil {
		return errResponse(req.ID, "internal", err.Error())
	}
	type ChannelJSON struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		Kind        string   `json:"kind"`
		Visibility  string   `json:"visibility"`
		Topic       string   `json:"topic"`
		MemberCount int      `json:"member_count"`
		Members     []string `json:"members"`
	}
	result := make([]ChannelJSON, 0, len(channels))
	for _, ch := range channels {
		result = append(result, ChannelJSON{
			ID: ch.ID, Name: ch.Name, Kind: ch.Kind,
			Visibility: ch.Visibility, Topic: ch.Topic,
			MemberCount: len(ch.Members), Members: ch.Members,
		})
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleChannelCreate(req Request) Response {
	var p struct {
		Name        string `json:"name"`
		Private     bool   `json:"private"`
		Description string `json:"description"`
	}
	json.Unmarshal(req.Params, &p)
	if p.Name == "" {
		return errResponse(req.ID, "invalid", "name is required")
	}
	vis := "public"
	if p.Private {
		vis = "private"
	}
	ch, err := h.Store.CreateChannel(p.Name, "channel", vis, p.Description, req.Auth.SessionID)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	return okResponse(req.ID, map[string]any{"id": ch.ID, "name": ch.Name})
}

func (h *Handler) handleChannelJoin(req Request) Response {
	var p struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(req.Params, &p)
	ch, err := h.Store.ChannelByName(p.Channel)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	if err := h.Store.Join(ch.ID, req.Auth.SessionID); err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	return okResponse(req.ID, map[string]any{"id": ch.ID})
}

func (h *Handler) handleChannelLeave(req Request) Response {
	var p struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(req.Params, &p)
	ch, err := h.Store.ChannelByName(p.Channel)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	if err := h.Store.Leave(ch.ID, req.Auth.SessionID); err != nil {
		return errResponse(req.ID, "internal", err.Error())
	}
	return okResponse(req.ID, struct{}{})
}

func (h *Handler) handleChannelRuleAdd(req Request) Response {
	var p struct {
		Channel string `json:"channel"`
		Subject string `json:"subject"`
		Value   string `json:"value"`
	}
	json.Unmarshal(req.Params, &p)
	ch, err := h.Store.ChannelByName(p.Channel)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	if err := h.Store.AddRule(ch.ID, p.Subject, p.Value); err != nil {
		return errResponse(req.ID, "invalid", err.Error())
	}
	return okResponse(req.ID, struct{}{})
}

func (h *Handler) handleChannelRuleList(req Request) Response {
	var p struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(req.Params, &p)
	ch, err := h.Store.ChannelByName(p.Channel)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	rules, err := h.Store.Rules(ch.ID)
	if err != nil {
		return errResponse(req.ID, "internal", err.Error())
	}
	type RuleJSON struct {
		Subject string `json:"subject"`
		Value   string `json:"value"`
	}
	result := make([]RuleJSON, 0, len(rules))
	for _, r := range rules {
		result = append(result, RuleJSON{Subject: r.Subject, Value: r.Value})
	}
	return okResponse(req.ID, result)
}

func (h *Handler) handleDM(req Request) Response {
	var p struct {
		With string `json:"with"`
	}
	json.Unmarshal(req.Params, &p)
	if p.With == "" {
		return errResponse(req.ID, "invalid", "with is required")
	}
	target, err := h.resolveRef(p.With, req.Auth.SessionID)
	if err != nil {
		return errResponse(req.ID, errorCode(err), err.Error())
	}
	ch, err := h.Store.DMChannel(req.Auth.SessionID, target)
	if err != nil {
		return errResponse(req.ID, "internal", err.Error())
	}
	return okResponse(req.ID, map[string]any{"channel_id": ch.ID, "name": ch.Name})
}

func (h *Handler) handleDispatch(req Request) Response {
	return errResponse(req.ID, "invalid", "dispatch not yet implemented on Go server")
}

// ── helpers ──────────────────────────────────────────────────────────────

type MessageJSON struct {
	ID           int64  `json:"id"`
	ChannelID    int64  `json:"channel_id"`
	ChannelName  string `json:"channel"`
	ThreadID     int64  `json:"thread_id"`
	From         string `json:"from_session"`
	FromName     string `json:"from_name"`
	Body         string `json:"body"`
	Hops         int    `json:"hops"`
	ExpectReply  bool   `json:"expect_reply"`
	ReplyToID    int64  `json:"reply_to_id,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	ReplyCommand string `json:"reply_command"`
}

func messageToJSON(m store.Message) MessageJSON {
	return MessageJSON{
		ID:           m.ID,
		ChannelID:    m.ChannelID,
		ChannelName:  m.ChannelName,
		ThreadID:     m.ThreadID,
		From:         m.From,
		FromName:     m.FromName,
		Body:         m.Body,
		Hops:         m.Hops,
		ExpectReply:  m.ExpectReply,
		ReplyToID:    m.ReplyToID,
		CreatedAt:    m.CreatedAt,
		ReplyCommand: fmt.Sprintf("tmux-agent-mesh reply --to-message %d --message \"...\"", m.ID),
	}
}

func (h *Handler) resolveRef(ref, callerID string) (string, error) {
	if ref == "" {
		return callerID, nil
	}
	return h.Store.Resolve(ref)
}

func okResponse(id string, result any) Response {
	return Response{ID: id, OK: true, Result: result}
}

func errResponse(id, code, msg string) Response {
	return Response{ID: id, OK: false, Error: &Error{Code: code, Message: msg}}
}

func errorCode(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "not found") || strings.Contains(s, "no agent"):
		return "not_found"
	case strings.Contains(s, "ambiguous"):
		return "ambiguous"
	case strings.Contains(s, "not a member") || strings.Contains(s, "forbidden") ||
		strings.Contains(s, "refused") || strings.Contains(s, "no access"):
		return "forbidden"
	case strings.Contains(s, "required") || strings.Contains(s, "must be"):
		return "invalid"
	default:
		return "internal"
	}
}
