package agents

import (
	"encoding/json"
	"slices"
	"strings"
)

type loopCatalogueEntry struct {
	ToolPath        string          `json:"tool_path"`
	Schema          int             `json:"schema"`
	Description     string          `json:"description"`
	ArgumentsSchema json.RawMessage `json:"arguments_schema"`
	ResultSchema    json.RawMessage `json:"result_schema"`
}
type loopCataloguePage struct {
	Entries []loopCatalogueEntry `json:"entries"`
	Next    string               `json:"next,omitempty"`
}

func (r *loopRuntime) catalogue(t *loopTurn, inv *loopInvocation, args loopCoreArgs) (any, uint64, error) {
	s := r.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(t.ctx, s.store.Durable()); err != nil {
		return nil, 0, err
	}
	chain, err := s.currentChain(t.ctx, t.origin, inv.grant, s.p.through)
	if err != nil {
		return nil, 0, err
	}
	allowed := make([]modelTool, 0, len(modelCatalogue))
	for _, tool := range modelCatalogue {
		if !s.loopCatalogueAllowed(t.ctx, t.origin, inv.grant, tool, chain, s.p.through) {
			continue
		}
		if !strings.Contains(tool.name, args.Query) && !strings.Contains(tool.description, args.Query) {
			continue
		}
		allowed = append(allowed, tool)
	}
	slices.SortFunc(allowed, func(a, b modelTool) int { return strings.Compare(a.name, b.name) })
	page := loopCataloguePage{Entries: []loopCatalogueEntry{}}
	limit := min(args.Limit, int(t.policy.MaxDiscoveryEntries))
	for _, tool := range allowed {
		if tool.name <= args.After {
			continue
		}
		if len(page.Entries) == limit {
			page.Next = page.Entries[len(page.Entries)-1].ToolPath
			break
		}
		entry := loopCatalogueEntry{ToolPath: tool.name, Schema: 1, Description: tool.description, ArgumentsSchema: modelSchemaJSON(tool.schema), ResultSchema: loopResultSchema(tool.name)}
		page.Entries = append(page.Entries, entry)
		// A page boundary is always a complete entry. If even one entry does
		// not fit, refuse rather than advertising a cursor that cannot advance.
		if int64(len(modelJSON(page)))+256 > t.policy.MaxResultBytes {
			page.Entries = page.Entries[:len(page.Entries)-1]
			if len(page.Entries) == 0 {
				return nil, 0, ErrTooLarge
			}
			page.Next = page.Entries[len(page.Entries)-1].ToolPath
			break
		}
	}
	return page, 0, nil
}
