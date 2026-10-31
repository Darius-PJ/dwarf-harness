package terminal

import (
	"fmt"
	"io"
	"strings"
	"uuid"
)

// ScopeBinding is one credential binding performed at startup: the version
// this launch bound and the scope's other active versions, which the owner
// may name as revoke_previous_version next time.
type ScopeBinding struct {
	ScopeID  uuid.UUID
	Provider string
	Version  uuid.UUID
	Others   []uuid.UUID
}

// WriteStartup reports each enabled context and each startup binding. It
// prints identities, versions and limits only: never a credential source kind,
// source locator or anything read from a source.
func WriteStartup(w io.Writer, s Settings, bound []ScopeBinding) error {
	var out strings.Builder
	for _, b := range bound {
		others := "none"
		if len(b.Others) != 0 {
			ids := make([]string, len(b.Others))
			for i, id := range b.Others {
				ids[i] = id.String()
			}
			others = strings.Join(ids, " ")
		}
		out.WriteString(render("[terminal]", "credential", fmt.Sprintf("scope %s provider %s bound version %s; other active versions: %s", b.ScopeID, b.Provider, b.Version, others)))
	}
	for _, c := range s.Contexts {
		task := "none"
		if c.TaskBinding != nil {
			task = c.TaskBinding.TaskID.String()
		}
		p := c.Policy
		out.WriteString(render(lineIDs{agent: c.AgentID, context: c.ContextID}.label(), "enabled", fmt.Sprintf("%q route %s version %d scope %s max_output_tokens %d task %s model_calls %d tool_calls %d timeout_ms %d live_bytes %d", c.DisplayTitle, c.RouteID, c.ExpectedRouteVersion, c.CredentialScopeID, c.MaxOutputTokens, task, p.MaxModelCalls, p.MaxToolCalls, p.TimeoutMS, p.MaxLiveBytes)))
	}
	_, err := io.WriteString(w, out.String())
	return err
}
