package dashboard

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
)

// clientRow is one detected MCP client as the dashboard reports it.
// Connected mirrors clients.Client.Injected: our mcpServers entry is
// present in that client's config. Broken repeats the doctor's clients
// check verdict for this one client, so the dashboard and `doctor` never
// disagree about the same config file.
type clientRow struct {
	Name       string `json:"name"`
	ConfigPath string `json:"config_path"`
	Installed  bool   `json:"installed"`
	Connected  bool   `json:"connected"`
	Transport  string `json:"transport"` // "http", "stdio", or "" when not connected
	Target     string `json:"target"`    // the URL (http) or command (stdio) the entry points at
	Broken     bool   `json:"broken"`
}

// handleClients serves the detected-client list and, on POST, adds our
// entry to one of them.
func (h *Handler) handleClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.writeJSON(w, http.StatusOK, h.detectClients())
	case http.MethodPost:
		h.mutating(h.handleClientAdd)(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// detectClients resolves every known client under the configured home and
// annotates each with how its entry (if any) is configured.
func (h *Handler) detectClients() []clientRow {
	found := clients.Detect(h.deps.Home)
	out := make([]clientRow, 0, len(found))
	for _, c := range found {
		row := clientRow{
			Name:       c.Name,
			ConfigPath: c.ConfigPath,
			Installed:  c.Installed,
			Connected:  c.Injected,
		}
		if c.Injected {
			cmd, target, ok := clients.InjectedEntry(c.ConfigPath)
			switch {
			case !ok:
				row.Broken = true
			case target != "":
				row.Transport, row.Target = "http", target
			default:
				row.Transport, row.Target = "stdio", cmd
				// Same test the doctor's clients check applies: an entry
				// naming a binary that is not this one, or is no longer on
				// disk, will fail to start when the client next launches.
				if cmd != h.deps.BinaryPath {
					row.Broken = true
				} else if _, err := os.Stat(cmd); err != nil {
					row.Broken = true
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// handleClientAdd injects our entry into the named client's config. The
// caller names a client, never a path: the config file is resolved from
// the known-client table so the dashboard cannot be talked into writing
// JSON to an arbitrary file on disk.
func (h *Handler) handleClientAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	target, ok := h.clientByName(in.Name)
	if !ok {
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown client"})
		return
	}
	if err := h.injectInto(target.ConfigPath); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"added": target.Name})
}

// handleClientRemove deletes our entry from the named client's config.
func (h *Handler) handleClientRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/clients/"))
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad client name"})
		return
	}
	target, ok := h.clientByName(name)
	if !ok {
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown client"})
		return
	}
	if err := clients.Remove(target.ConfigPath); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"removed": target.Name})
}

// clientByName looks a detected client up by its display name.
func (h *Handler) clientByName(name string) (clients.Client, bool) {
	for _, c := range clients.Detect(h.deps.Home) {
		if c.Name == name {
			return c, true
		}
	}
	return clients.Client{}, false
}

// injectInto writes our entry into one config file, choosing the
// transport that matches how this process is reachable. The dashboard only
// exists when serve was started with --http, so the HTTP entry is the
// normal path; the stdio fallback covers a handler constructed without a
// URL (tests, and any future non-HTTP embedding).
func (h *Handler) injectInto(configPath string) error {
	if h.deps.HTTPURL != "" {
		return clients.InjectHTTP(configPath, h.deps.HTTPURL, h.deps.Token)
	}
	return clients.Inject(configPath, h.deps.BinaryPath)
}
