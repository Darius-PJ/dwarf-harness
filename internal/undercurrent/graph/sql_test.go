package graph

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func analysisFixture(files map[string]string) AnalysisRequest {
	r := AnalysisRequest{AnalysisKey: digest("analysis"), ConfigurationSHA256: digest("config"), Allowance: 10000, Source: ledger.GraphSource{WorkspaceID: testID(), Registered: ledger.RecordRef{Seq: 1, Hash: digest("registered")}, Subject: ledger.RecordRef{Seq: 2, Hash: digest("source")}, Tree: strings.Repeat("a", 40)}}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		k, _ := ArtifactKey(testID(), 1, p)
		r.Corpus = append(r.Corpus, CorpusArtifact{Path: p, Key: k, SHA256: digest(files[p]), Bytes: []byte(files[p])})
	}
	return r
}

func TestSQLSubsetAndUnknownCoverage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, query, reason string
		edges               int
	}{
		{"plain", "SELECT id, name FROM public.people;", "", 1},
		{"comments", "/* FROM missing; */ SELECT * -- FROM fake\n FROM PUBLIC.people;", "", 1},
		{"quoted", "SELECT \"Id\" FROM \"Public\".\"People\";", "", 1},
		{"doubled quote", `SELECT * FROM "Peo""ple";`, "", 1},
		{"quoted mismatch", "SELECT * FROM public.\"People\";", ledger.ReasonUnresolvedReference, 0},
		{"join", "SELECT * FROM public.people JOIN others;", ledger.ReasonUnsupportedSyntax, 0},
		{"alias", "SELECT * FROM public.people p;", ledger.ReasonUnsupportedSyntax, 0},
		{"nested", "SELECT * FROM (SELECT * FROM public.people);", ledger.ReasonUnsupportedSyntax, 0},
		{"dynamic", "EXECUTE 'SELECT * FROM public.people';", ledger.ReasonUnsupportedSyntax, 0},
		{"string", "SELECT 'FROM public.people' FROM public.people;", ledger.ReasonUnsupportedSyntax, 0},
		{"quote", "SELECT * FROM \"people;", ledger.ReasonUnsupportedSyntax, 0},
		{"comment", "SELECT * FROM public.people; /*", ledger.ReasonUnsupportedSyntax, 0},
		{"missing", "SELECT * FROM absent;", ledger.ReasonUnresolvedReference, 0},
		{"partial", "SELECT * FROM public.people; DELETE FROM public.people;", ledger.ReasonUnsupportedSyntax, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := analysisFixture(map[string]string{"schema.sql": "CREATE TABLE public.people (id INTEGER, name TEXT); CREATE TABLE \"Public\".\"People\" (\"Id\" INTEGER); CREATE TABLE \"Peo\"\"ple\" (id INT);", "query.sql": tc.query})
			out, err := AnalyzeSQL(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			query := r.Corpus[0]
			found := false
			for _, c := range out.Coverage {
				if c.Scope == keyScope(query.Key) {
					found = true
					want := covered(query.Key, ledger.AdapterSQL, tc.reason)
					a, _ := json.Marshal(c)
					b, _ := json.Marshal(want)
					if string(a) != string(b) {
						t.Fatalf("coverage %s want %s", a, b)
					}
				}
			}
			if !found {
				t.Fatal("missing query coverage")
			}
			if len(out.Edges) != tc.edges {
				t.Fatalf("edges %+v", out.Edges)
			}
			for _, e := range out.Edges {
				if e.From.Kind != ledger.NodeKindModule || e.To != query.Key || e.Evidence.Artifact != query.Key || e.Evidence.ArtifactSHA256 != query.SHA256 {
					t.Fatalf("not evidenced dependency to consumer: %+v", e)
				}
			}
		})
	}
	r := analysisFixture(map[string]string{"a.sql": "CREATE TABLE x (id INT);", "b.sql": "CREATE TABLE x (id INT);", "q.sql": "SELECT * FROM x;"})
	out, err := AnalyzeSQL(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Edges) != 0 {
		t.Fatal("ambiguous table resolved")
	}
	for _, c := range out.Coverage {
		if c.State != ledger.CoverageUnknown || len(c.Reasons) != 1 || c.Reasons[0] != ledger.ReasonUnresolvedReference {
			t.Fatalf("ambiguous coverage %+v", c)
		}
	}
	r = analysisFixture(map[string]string{"s.sql": "CREATE TABLE x (id INT); SELECT * FROM x;"})
	r.Allowance = 1
	out, err = AnalyzeSQL(context.Background(), r)
	if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || out.Coverage[0].Reasons[0] != ledger.ReasonWorkExhausted {
		t.Fatalf("partial on limit %+v %v", out, err)
	}
}

func TestSQLLiteralProofIsExactAndDeterministic(t *testing.T) {
	t.Parallel()
	literal := "public /*actual*/ . PEOPLE"
	query := "SELECT * FROM " + literal + ";"
	analyze := func(query string) ledger.GraphEdge {
		t.Helper()
		out, err := AnalyzeSQL(context.Background(), analysisFixture(map[string]string{
			"schema.sql": "CREATE TABLE public.people (id INT);",
			"query.sql":  query,
		}))
		if err != nil || len(out.Edges) != 1 {
			t.Fatalf("resolved proof %+v %v", out, err)
		}
		return out.Edges[0]
	}
	single := analyze(query)
	if single.Evidence.Reference != literal {
		t.Fatalf("reference %q want literal %q", single.Evidence.Reference, literal)
	}
	second := "SELECT * FROM PUBLIC.people;"
	for _, combined := range []string{query + second, second + query} {
		edge := analyze(combined)
		if edge.EdgeID != single.EdgeID || edge.Evidence.Reference != "PUBLIC.people" {
			t.Fatalf("proof not canonical across source order: %+v", edge)
		}
	}
}

func FuzzSQLSubset(f *testing.F) {
	for _, seed := range []string{
		"", "CREATE TABLE x (a INT); SELECT * FROM x;", "CREATE TABLE \"a\".\"b\" (\"c\" T);",
		"--comment\nSELECT x FROM missing;", "/* open", "SELECT 'FROM x';",
		"SELECT * FROM x JOIN y;", "SELECT * FROM x z;", "SELECT * FROM (SELECT * FROM x);",
		"EXECUTE 'x';", "SELECT * FROM \"bad", "SELECT * FROM x; DELETE FROM x;",
		"/*closed*/ CREATE TABLE \"a\"\"b\" (id INT, name TEXT); SELECT id,name FROM \"a\"\"b\";",
		"CREATE TABLE x (a INT); CREATE TABLE x (b INT); SELECT * FROM x;",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > maxBlob {
			return
		}
		r := analysisFixture(map[string]string{"query.sql": s})
		out, err := AnalyzeSQL(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if out.WorkUsed > r.Allowance {
			t.Fatal("over budget")
		}
		for _, c := range out.Coverage {
			if !coverageValid(c) {
				t.Fatalf("invalid coverage %+v", c)
			}
		}
		for _, e := range out.Edges {
			if !edgeValid(e) {
				t.Fatal("invalid edge")
			}
		}
	})
}

func TestCorpusLimitsAreUnknownNotPartial(t *testing.T) {
	t.Parallel()
	exact := analysisFixture(map[string]string{"comment.sql": "--" + strings.Repeat("x", maxBlob-2)})
	out, err := AnalyzeSQL(context.Background(), exact)
	if err != nil || len(out.Coverage) != 1 || out.Coverage[0].State != ledger.CoverageComplete {
		t.Fatalf("exact blob cap %+v %v", out, err)
	}
	tooLarge := analysisFixture(map[string]string{"comment.sql": "--" + strings.Repeat("x", maxBlob-1)})
	for _, analyze := range []func(context.Context, AnalysisRequest) (AnalysisResult, error){AnalyzeSQL, AnalyzeOpaque} {
		out, err = analyze(context.Background(), tooLarge)
		if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || len(out.Coverage) != 1 || out.Coverage[0].Reasons[0] != ledger.ReasonHelperLimit {
			t.Fatalf("oversized corpus %+v %v", out, err)
		}
	}
	tooLarge.ConfigurationSHA256 = ConfigurationDigest(Tools{})
	out, err = NewAnalyzer(Tools{}).Analyze(context.Background(), ledger.AdapterPython, tooLarge)
	if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || out.Coverage[0].Reasons[0] != ledger.ReasonHelperLimit {
		t.Fatalf("helper oversized input %+v %v", out, err)
	}
}
