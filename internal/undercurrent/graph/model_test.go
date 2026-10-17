package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testID() uuid.UUID             { return uuid.MustParse("01900000-0000-7000-8000-000000000001") }
func digest(s string) ledger.Digest { return ledger.Digest(sha256.Sum256([]byte(s))) }

func TestCanonicalIdentityAndState(t *testing.T) {
	t.Parallel()
	key, err := ArtifactKey(testID(), 7, "a<&.py")
	if err != nil {
		t.Fatal(err)
	}
	if key.LocalKey != `[7,"a\u003c\u0026.py"]` {
		t.Fatalf("local key %s", key.LocalKey)
	}
	module, err := ModuleKey(testID(), "python", "workspace", "pkg.x")
	if err != nil {
		t.Fatal(err)
	}
	if module.LocalKey != `["python","workspace","pkg.x"]` {
		t.Fatal(module)
	}
	// Independent literal wire vector: both endpoints use the frozen member order.
	wire := `[{"namespace":"workspace","origin_id":"01900000-0000-7000-8000-000000000001","kind":"module","local_key":"[\"python\",\"workspace\",\"pkg.x\"]"},{"namespace":"workspace","origin_id":"01900000-0000-7000-8000-000000000001","kind":"artifact","local_key":"[7,\"a\\u003c\\u0026.py\"]"},"static_dependency"]`
	if got := ledger.EdgeID(module, key, "static_dependency"); got != digest(wire) {
		t.Fatalf("edge vector %s", got)
	}
	raw, got, err := (State{}).Canonical()
	if err != nil || string(raw) != `{"scopes":[],"nodes":[],"edges":[],"coverage":[]}` || got != ledger.EmptyGraphDigest {
		t.Fatalf("empty %s %s %v", raw, got, err)
	}
	members := []ModuleMember{{"z.py", digest("z")}, {"a.py", digest("a")}}
	want := digest(`{"module_name":"pkg","members":[["a.py","` + digest("a").String() + `"],["z.py","` + digest("z").String() + `"]]}`)
	version, err := ModuleVersion("pkg", members)
	if err != nil || version != want {
		t.Fatalf("module vector %s %v", version, err)
	}
	if members[0].Path != "z.py" {
		t.Fatal("mutated caller members")
	}
	state := State{Nodes: []ledger.GraphNode{{Key: key, VersionSHA256: digest("v"), Adapter: ledger.AdapterOpaque, ConfigurationSHA256: digest("c")}, {Key: module, VersionSHA256: version, Adapter: ledger.AdapterPython, ConfigurationSHA256: digest("c")}}}
	first, sum, err := state.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	state.Nodes[0], state.Nodes[1] = state.Nodes[1], state.Nodes[0]
	second, sum2, err := state.Canonical()
	if err != nil || !bytes.Equal(first, second) || sum != sum2 {
		t.Fatal("order changed canonical state")
	}
	state.Nodes = append(state.Nodes, state.Nodes[0])
	if _, _, err := state.Canonical(); err == nil {
		t.Fatal("accepted duplicate identity")
	}
	for _, path := range []string{"", "/a", "a/", "a//b", "a/../b", "a\\b", "a\x00b", string([]byte{255}), strings.Repeat("x", 4096)} {
		if _, err := ArtifactKey(testID(), 7, path); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
}

func TestArtifactIdentityUsesBirthNotContent(t *testing.T) {
	t.Parallel()
	a, _ := ArtifactKey(testID(), 7, "file.txt")
	recreated, _ := ArtifactKey(testID(), 9, "file.txt")
	renamed, _ := ArtifactKey(testID(), 9, "other.txt")
	other, _ := ArtifactKey(uuid.MustParse("01900000-0000-7000-8000-000000000002"), 7, "file.txt")
	if a == recreated || a == renamed || a == other {
		t.Fatal("generation, path or workspace aliases")
	}
	before := analysisFixture(map[string]string{"file.txt": "before"})
	first, err := AnalyzeOpaque(context.Background(), before)
	if err != nil {
		t.Fatal(err)
	}
	after := analysisFixture(map[string]string{"file.txt": "after"})
	second, err := AnalyzeOpaque(context.Background(), after)
	if err != nil {
		t.Fatal(err)
	}
	if first.Nodes[0].Key != second.Nodes[0].Key || first.Nodes[0].VersionSHA256 == second.Nodes[0].VersionSHA256 {
		t.Fatal("content changed identity or failed to change version")
	}
}
