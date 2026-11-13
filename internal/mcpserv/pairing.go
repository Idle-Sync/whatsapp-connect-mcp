package mcpserv

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NotPairedMessage is the error every tool call except doctor returns
// while this install has no WhatsApp pairing.
const NotPairedMessage = "not paired: this install has no WhatsApp pairing yet. Run " +
	"`whatsapp-connect-mcp setup` in a terminal and scan the QR code with the phone; the " +
	"tools start working as soon as pairing completes, with no restart. The doctor tool " +
	"works now and reports the same thing."

// RequirePairing makes every tool call except doctor fail with
// NotPairedMessage while needsPairing reports true. initialize and
// tools/list are untouched, so a client set up before pairing still sees
// the full tool surface instead of a server that never answers. The check
// runs on every call, so the guard lifts by itself the moment pairing lands.
func RequirePairing(server *mcp.Server, needsPairing func() bool) {
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" && needsPairing() {
				if call, ok := req.(*mcp.CallToolRequest); ok && call.Params.Name != "doctor" {
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: NotPairedMessage}},
					}, nil
				}
			}
			return next(ctx, method, req)
		}
	})
}
