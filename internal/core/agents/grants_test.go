package agents

import (
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func ruleGrant() grantRules {
	b := Binding{privateID(1), privateID(2)}
	return grantRules{
		id: privateID(10), grantee: b,
		operations: []string{"file.read", "file.write", "grant.issue", "grant.revoke"},
		resources:  []ledger.Resource{{Kind: "workspace", Binding: ledger.Binding(b), WorkspaceID: privateID(3), PathPrefix: "a"}},
		dataClass:  "workspace", recipients: []Binding{b, {privateID(4), privateID(5)}},
		limits: ledger.Capacity{TurnSlots: 3, LocalSlots: 4, BufferBytes: 100, LocalUnits: 10, MaxOperationBytes: 50},
		depth:  3, lifetime: "until_revoked",
	}
}

func TestScopedGrantAttenuation(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		change  func(*grantRules, *grantRules)
		allowed bool
	}{
		{"equal_region", func(p, c *grantRules) {}, true},
		{"narrow", func(p, c *grantRules) {
			c.operations = []string{"file.read"}
			c.resources[0].PathPrefix = "a/b"
			c.recipients = c.recipients[:1]
			c.limits.LocalUnits = 1
		}, true},
		{"operation", func(p, c *grantRules) { c.operations = []string{"file.edit"} }, false},
		{"resource", func(p, c *grantRules) { c.resources[0].PathPrefix = "other" }, false},
		{"recipient", func(p, c *grantRules) { c.recipients = []Binding{{privateID(8), privateID(9)}} }, false},
		{"grantee", func(p, c *grantRules) { c.grantee = Binding{privateID(8), privateID(9)} }, false},
		{"class", func(p, c *grantRules) { c.dataClass = "private" }, false},
		{"same_depth", func(p, c *grantRules) { c.depth = p.depth }, false},
		{"negative_depth", func(p, c *grantRules) { c.depth = -1 }, false},
		{"no_delegation", func(p, c *grantRules) { p.operations = []string{"file.read", "file.write"} }, false},
		{"turn_limit", func(p, c *grantRules) { c.limits.TurnSlots++ }, false},
		{"local_limit", func(p, c *grantRules) { c.limits.LocalSlots++ }, false},
		{"buffer_limit", func(p, c *grantRules) { c.limits.BufferBytes++ }, false},
		{"unit_limit", func(p, c *grantRules) { c.limits.LocalUnits++ }, false},
		{"byte_limit", func(p, c *grantRules) { c.limits.MaxOperationBytes++ }, false},
		{"negative_limit", func(p, c *grantRules) { c.limits.LocalUnits = -1 }, false},
		{"unsorted_operations", func(p, c *grantRules) { slices.Reverse(c.operations) }, false},
		{"duplicate_operations", func(p, c *grantRules) { c.operations = []string{"file.read", "file.read"} }, false},
		{"duplicate_resources", func(p, c *grantRules) { c.resources = append(c.resources, c.resources[0]) }, false},
		{"duplicate_recipients", func(p, c *grantRules) { c.recipients = append(c.recipients[:1], c.recipients[0]) }, false},
		{"new_task_binding", func(p, c *grantRules) {
			c.task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(21), Generation: 1}
		}, true},
		{"inherited_task", func(p, c *grantRules) {
			p.task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(21), Generation: 1}
			c.task = p.task
		}, true},
		{"removed_task", func(p, c *grantRules) {
			p.task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(21), Generation: 1}
		}, false},
		{"changed_generation", func(p, c *grantRules) {
			p.task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(21), Generation: 1}
			c.task = p.task
			c.task.Generation++
		}, false},
		{"unknown_lifetime", func(p, c *grantRules) { c.lifetime = "until_tomorrow" }, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			parent := ruleGrant()
			child := ruleGrant()
			child.id, child.parent, child.depth = privateID(11), parent.id, 2
			row.change(&parent, &child)
			if got := grantAttenuates(parent, child); got != row.allowed {
				t.Fatalf("attenuation = %v, want %v", got, row.allowed)
			}
		})
	}
}

func TestScopedGrantChain(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		change  func(*[]grantRules, *authorityRequest)
		allowed bool
	}{
		{"current", func(c *[]grantRules, r *authorityRequest) {}, true},
		{"revoked_ancestor", func(c *[]grantRules, r *authorityRequest) { (*c)[1].revoked = true }, false},
		{"future_lifetime", func(c *[]grantRules, r *authorityRequest) { (*c)[1].lifetime = "timed" }, false},
		{"missing_root", func(c *[]grantRules, r *authorityRequest) { *c = (*c)[:1] }, false},
		{"wrong_leaf", func(c *[]grantRules, r *authorityRequest) { r.grant = privateID(99) }, false},
		{"wrong_origin", func(c *[]grantRules, r *authorityRequest) { r.origin.ContextID = privateID(99) }, false},
		{"wrong_operation", func(c *[]grantRules, r *authorityRequest) { r.operation = "model.call" }, false},
		{"wrong_target", func(c *[]grantRules, r *authorityRequest) { r.targets[0].PathPrefix = "b" }, false},
		{"wrong_class", func(c *[]grantRules, r *authorityRequest) { r.dataClass = "external" }, false},
		{"stale_task", func(c *[]grantRules, r *authorityRequest) {
			(*c)[0].task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(21), Generation: 1}
			r.task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(22), Generation: 2}
		}, false},
		{"current_task", func(c *[]grantRules, r *authorityRequest) {
			(*c)[0].task = ledger.TaskBinding{TaskID: privateID(20), AssignmentID: privateID(21), Generation: 1}
			r.task = (*c)[0].task
		}, true},
		{"recipient", func(c *[]grantRules, r *authorityRequest) { r.recipients = []Binding{{privateID(90), privateID(91)}} }, false},
		{"byte_ceiling", func(c *[]grantRules, r *authorityRequest) { r.bytes = 51 }, false},
		{"split_grants_no_union", func(c *[]grantRules, r *authorityRequest) {
			(*c)[0].parent = uuid.UUID{}
			(*c)[0].operations = []string{"file.read"}
			(*c)[1].operations = []string{"file.write"}
			(*c)[1].resources[0].PathPrefix = "other"
			r.operation = "file.write"
		}, false},
		{"delegation_not_data_access", func(c *[]grantRules, r *authorityRequest) {
			other := ledger.Binding{AgentID: privateID(4), ContextID: privateID(5)}
			for i := range *c {
				(*c)[i].resources[0].Binding = other
			}
			r.targets[0].Binding = other
		}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, leaf := ruleGrant(), ruleGrant()
			leaf.id, leaf.parent, leaf.depth = privateID(11), root.id, 2
			chain := []grantRules{leaf, root}
			req := authorityRequest{grant: leaf.id, origin: leaf.grantee, operation: "file.read", dataClass: "workspace", targets: slices.Clone(leaf.resources), bytes: 10}
			row.change(&chain, &req)
			if got := chainAuthorizes(chain, req); got != row.allowed {
				t.Fatalf("chain authorization = %v, want %v", got, row.allowed)
			}
		})
	}
}

func TestScopedTaskCreationRegion(t *testing.T) {
	t.Parallel()
	g := ruleGrant()
	g.dataClass, g.operations, g.resources = "task", []string{"grant.issue", "task.create"}, []ledger.Resource{{Kind: "task"}}
	child := g
	child.id, child.parent, child.depth = privateID(11), g.id, 1
	child.resources = []ledger.Resource{{Kind: "task", TaskID: privateID(50)}}
	if !grantAttenuates(g, child) {
		t.Fatal("creation grant cannot add an exact task")
	}
	g.operations = []string{"task.inspect"}
	if validGrant(g) {
		t.Fatal("unnamed task resource granted task inspection")
	}
}

func TestScopedExternalOperationSeparation(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"shell.execute", "process.spawn", "network.connect", "connector.call", "model.call"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			g := ruleGrant()
			request := authorityRequest{grant: g.id, origin: g.grantee, operation: op, dataClass: "workspace", targets: slices.Clone(g.resources)}
			if chainAuthorizes([]grantRules{g}, request) {
				t.Fatal("local grant authorized external effect")
			}
			g.dataClass, g.operations, g.resources = "external", []string{op}, []ledger.Resource{{Kind: "external", ResourceKey: "producer_key"}}
			request.dataClass, request.targets = "external", slices.Clone(g.resources)
			if !chainAuthorizes([]grantRules{g}, request) {
				t.Fatal("separate external authority refused")
			}
			other := "shell.execute"
			if op == other {
				other = "model.call"
			}
			g.operations = []string{op, other}
			slices.Sort(g.operations)
			if validGrant(g) {
				t.Fatal("combined external classes accepted")
			}
		})
	}
}
