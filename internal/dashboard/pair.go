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

func (h *Handler) handlePairStart(w http.ResponseWriter, _ *http.Request) {
	if !h.deps.Bridge.NeedsPairing() {
		h.writeJSON(w, http.StatusConflict, map[string]string{"error": "already paired"})
		return
	}
	h.pair.mu.Lock()
	if h.pair.active {
		h.pair.mu.Unlock()
		h.writeJSON(w, http.StatusOK, map[string]string{"status": "pairing already in progress"})
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
