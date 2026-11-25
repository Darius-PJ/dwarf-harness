package terminal

import (
	"math"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// MaxLineBytes bounds one input line, including a /send command.
const MaxLineBytes = 2 << 20

// DecodeCommand strictly decodes /send's command JSON into one schema-1
// session.Command. JSON string escapes carry multiline text; nothing is
// evaluated by a shell. Core still validates every identity and value.
func DecodeCommand(raw []byte) (session.Command, error) {
	r := &reader{kind: ErrCommand}
	if len(raw) > MaxLineBytes {
		return session.Command{}, &RefusalError{Kind: ErrCommand, Path: "$", Allowed: "at most 2 MiB"}
	}
	root, err := parseStrict(raw)
	if err != nil {
		return session.Command{}, &RefusalError{Kind: ErrCommand, Path: "$", Allowed: "one UTF-8 JSON object without duplicate members, null, deep nesting or trailing data"}
	}
	if !r.object(root, "$", []string{"schema", "command_id", "context_id", "operation", "arguments"}, nil) {
		return session.Command{}, r.err
	}
	m := root.members
	c := session.Command{
		Schema:    int(r.integer(m["schema"], "schema", 1, 1)),
		CommandID: r.id(m["command_id"], "command_id"),
		ContextID: r.id(m["context_id"], "context_id"),
		Operation: r.choice(m["operation"], "operation", "message.send", "turn.cancel", "turn.status", "context.status", "promotion.request", "service.status", "service.outages"),
	}
	args := m["arguments"]
	switch c.Operation {
	case "message.send":
		if r.object(args, "arguments", []string{"turn_id", "text"}, []string{"artifacts"}) {
			send := session.MessageSend{TurnID: r.id(args.members["turn_id"], "arguments.turn_id"), Artifacts: []ledger.ArtifactRef{}}
			if text := args.members["text"]; text.kind == 's' {
				send.Text = text.text
			} else {
				r.fail("arguments.text", "a JSON string")
			}
			if list := args.members["artifacts"]; list != nil {
				for i, item := range r.array(list, "arguments.artifacts", 0) {
					path := index("arguments.artifacts", i)
					if !r.object(item, path, []string{"source_sequence", "sha256", "bytes"}, nil) {
						break
					}
					send.Artifacts = append(send.Artifacts, ledger.ArtifactRef{
						SourceSequence: r.integer(item.members["source_sequence"], join(path, "source_sequence"), 1, math.MaxInt64),
						SHA256:         r.digest(item.members["sha256"], join(path, "sha256")),
						Bytes:          r.integer(item.members["bytes"], join(path, "bytes"), 1, math.MaxInt64),
					})
				}
			}
			c.Arguments = send
		}
	case "turn.cancel", "turn.status":
		if r.object(args, "arguments", []string{"turn_id"}, nil) {
			turn := r.id(args.members["turn_id"], "arguments.turn_id")
			if c.Operation == "turn.cancel" {
				c.Arguments = session.TurnCancel{TurnID: turn}
			} else {
				c.Arguments = session.TurnStatus{TurnID: turn}
			}
		}
	case "context.status":
		if r.object(args, "arguments", nil, nil) {
			c.Arguments = session.ContextStatus{}
		}
	case "service.status":
		if r.object(args, "arguments", nil, []string{"agent_id"}) {
			q := session.ServiceStatus{}
			if v := args.members["agent_id"]; v != nil {
				q.AgentID = r.id(v, "arguments.agent_id")
			}
			c.Arguments = q
		}
	case "service.outages":
		if r.object(args, "arguments", nil, []string{"outage_id", "after", "limit", "through_sequence"}) {
			q := session.ServiceOutages{}
			if v := args.members["outage_id"]; v != nil {
				q.OutageID = r.id(v, "arguments.outage_id")
			}
			if v := args.members["limit"]; v != nil {
				q.Limit = int(r.integer(v, "arguments.limit", 1, 128))
			}
			if v := args.members["through_sequence"]; v != nil {
				q.ThroughSequence = uint64(r.integer(v, "arguments.through_sequence", 1, math.MaxInt64))
			}
			if v := args.members["after"]; v != nil && r.object(v, "arguments.after", []string{"outage_id", "agent_id", "through_sequence", "caller_id"}, []string{"filter_agent_id", "filter_cause_id", "filter_outage_id"}) {
				q.After = &session.ServiceOutageCursor{
					OutageID:        r.id(v.members["outage_id"], "arguments.after.outage_id"),
					AgentID:         r.id(v.members["agent_id"], "arguments.after.agent_id"),
					ThroughSequence: uint64(r.integer(v.members["through_sequence"], "arguments.after.through_sequence", 1, math.MaxInt64)),
					CallerID:        r.id(v.members["caller_id"], "arguments.after.caller_id"),
				}
				for name, target := range map[string]*uuid.UUID{"filter_agent_id": &q.After.FilterAgentID, "filter_cause_id": &q.After.FilterCauseID, "filter_outage_id": &q.After.FilterOutageID} {
					if value := v.members[name]; value != nil {
						*target = r.id(value, "arguments.after."+name)
					}
				}
			}
			c.Arguments = q
		}
	case "promotion.request":
		if r.object(args, "arguments", []string{"workspace_id", "head"}, []string{"restart_from"}) {
			p := session.PromotionRequest{WorkspaceID: r.id(args.members["workspace_id"], "arguments.workspace_id"), Head: r.id(args.members["head"], "arguments.head")}
			if v := args.members["restart_from"]; v != nil {
				p.RestartFrom = r.id(v, "arguments.restart_from")
			}
			c.Arguments = p
		}
	}
	if r.err != nil {
		return session.Command{}, r.err
	}
	return c, nil
}
