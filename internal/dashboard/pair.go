package dashboard

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"rsc.io/qr"
)

// pairState is the single-flight in-process pairing driver. One PairQR
// runs at a time; its show callback publishes the newest code here and
// qr.png renders whatever is current. Outcome (success clears
// NeedsPairing; failure records a category-only message) is visible via
// GET /api/pair.
type pairState struct {
	mu      sync.Mutex
	active  bool
	code    string
	lastErr string
}

// handlePairStart decides whether to join an in-flight pairing or start a
// new one entirely under h.pair.mu: the active-join check and the
// NeedsPairing check must be one atomic decision, or a request whose
// NeedsPairing read is stale by the time it acquires the lock can start a
// second PairQR against an already-paired bridge.
func (h *Handler) handlePairStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.pair.mu.Lock()
	if h.pair.active {
		h.pair.mu.Unlock()
		h.writeJSON(w, http.StatusOK, map[string]string{"status": "pairing already in progress"})
		return
	}
	if !h.deps.Bridge.NeedsPairing() {
		h.pair.mu.Unlock()
		h.writeJSON(w, http.StatusConflict, map[string]string{"error": "already paired"})
		return
	}
	h.pair.active = true
	h.pair.code = ""
	h.pair.lastErr = ""
	h.pair.mu.Unlock()

	go h.runPairing()
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "pairing started"})
}

// runPairing drives Bridge.PairQR on the process-scoped context: pairing
// outlives the HTTP request that started it. PairQR returns with the
// client connected on success.
func (h *Handler) runPairing() {
	err := h.deps.Bridge.PairQR(h.deps.Ctx, func(code string) {
		h.pair.mu.Lock()
		h.pair.code = code
		h.pair.mu.Unlock()
	})
	h.pair.mu.Lock()
	h.pair.active = false
	h.pair.code = ""
	if err != nil && !errors.Is(err, context.Canceled) {
		h.pair.lastErr = err.Error() // bridge errors are category-only by contract
	}
	h.pair.mu.Unlock()
}

// handlePairLogout unlinks this server from the account on WhatsApp's
// servers on the human's direct say-so — the UI arms the button with a
// second-click confirmation before this is ever called. It refuses while
// a pairing attempt is active (the two operations contradict each other)
// and when there is nothing to unlink.
func (h *Handler) handlePairLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.pair.mu.Lock()
	if h.pair.active {
		h.pair.mu.Unlock()
		h.writeJSON(w, http.StatusConflict, map[string]string{"error": "a pairing attempt is in progress"})
		return
	}
	h.pair.mu.Unlock()
	if h.deps.Bridge.NeedsPairing() {
		h.writeJSON(w, http.StatusConflict, map[string]string{"error": "not paired"})
		return
	}
	if err := h.deps.Bridge.Logout(r.Context()); err != nil {
		h.writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()}) // bridge errors are category-only by contract
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

func (h *Handler) handlePairInfo(w http.ResponseWriter, _ *http.Request) {
	h.pair.mu.Lock()
	resp := map[string]any{"pairing": h.pair.active, "has_code": h.pair.code != "", "error": h.pair.lastErr}
	h.pair.mu.Unlock()
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handlePairQR(w http.ResponseWriter, r *http.Request) {
	h.pair.mu.Lock()
	code := h.pair.code
	h.pair.mu.Unlock()
	if code == "" {
		http.NotFound(w, r)
		return
	}
	c, err := qr.Encode(code, qr.L)
	if err != nil {
		http.Error(w, "qr encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(c.PNG())
}
