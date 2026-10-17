package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"uuid"
)

// These fixtures exercise F-U3-61, ticket 61's part and U3's first producer
// family, frozen (D130), through public payload codecs.
func decodeU3_61(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeGraphAnalysisStarted:
		return DecodeGraphAnalysisStarted(js)
	case TypeGraphAnalysisProgress:
		return DecodeGraphAnalysisProgress(js)
	case TypeGraphDeltaChunk:
		return DecodeGraphDeltaChunk(js)
	case TypeGraphVersionCommitted:
		return DecodeGraphVersionCommitted(js)
	}
	panic("not an F-U3-61 type: " + typ)
}

func graphFixtureDigest(s string) Digest { return Digest(sha256.Sum256([]byte(s))) }

func graphFixtureKey(workspace uuid.UUID, kind, local string) NodeKey {
	return NodeKey{Namespace: NodeNamespaceWorkspace, OriginID: workspace, Kind: kind, LocalKey: local}
}

func graphFixtureEdge(from, to NodeKey, adapter, reference string) GraphEdge {
	return GraphEdge{EdgeID: EdgeID(from, to, "static_dependency"), From: from, To: to, Type: "static_dependency", Geometry: GeometryCoupling, WeightPPM: 1000000,
		Evidence: EdgeEvidence{Artifact: to, ArtifactSHA256: goldenB1, Adapter: adapter, ConfigurationSHA256: goldenB2, Reference: reference}}
}

func u3_61Payloads() []struct {
	name, typ string
	p         payload
} {
	start := GraphAnalysisStarted{
		AnalysisID: fid("18"), AnalysisKey: goldenB1,
		Source:        GraphSource{fid("07"), RecordRef{1, goldenB1}, RecordRef{2, goldenB2}, strings.Repeat("a", 40)},
		InputFrontier: RecordRef{2, goldenB2}, Scope: GraphScopeCanonical, BaseDigest: EmptyGraphDigest,
		AnalyzerEpoch: 1, ConfigurationSHA256: goldenB3, LimitsProfile: LimitsProfileV1, HelperProfile: HelperProfileV1, Reason: GraphReasonLoad,
	}
	incremental, future := start, start
	incremental.BaseVersion, incremental.BaseDigest, incremental.Reason = math.MaxInt64, goldenB1, GraphReasonIncremental
	incremental.Source.Tree, incremental.AnalyzerEpoch = strings.Repeat("b", 64), math.MaxInt64
	incremental.Source.Subject.Seq, incremental.InputFrontier.Seq = math.MaxInt64, math.MaxInt64
	future.Reason = "future_reason"
	future.Scope = strings.Repeat("s", 64)
	future.LimitsProfile, future.HelperProfile = strings.Repeat("a", 62)+"/1", strings.Repeat("b", 62)+"/1"
	reserved := GraphAnalysisProgress{AnalysisID: start.AnalysisID, AnalysisKey: goldenB1, Revision: 1, Phase: GraphPhaseReserved, Coverage: []CoverageEntry{}}
	checkpoint, unknown, superseded := reserved, reserved, reserved
	checkpoint.Phase, checkpoint.Revision, checkpoint.ReservedTotal = GraphPhaseCheckpoint, math.MaxInt64, math.MaxInt64
	checkpoint.VerifiedCursor = GraphCursor{math.MaxInt64, math.MaxInt64}
	unknown.Phase = GraphPhaseUnknown
	unknown.Coverage = []CoverageEntry{{"workspace:fixture:canonical", AdapterPython, CoverageUnknown, []string{ReasonHelperLimit, ReasonRecoveryUnknown}}}
	superseded.Phase, superseded.Coverage = GraphPhaseSuperseded, unknown.Coverage
	empty := GraphDeltaChunk{AnalysisID: start.AnalysisID, AnalysisKey: goldenB1, RemoveEdges: []Digest{}, RemoveNodes: []NodeKey{}, PutNodes: []GraphNode{}, PutEdges: []GraphEdge{}, Coverage: []CoverageEntry{}}
	full := empty
	full.ChunkIndex = math.MaxInt64
	one, two := graphFixtureKey(fid("07"), NodeKindArtifact, "a"), graphFixtureKey(fid("07"), NodeKindArtifact, "b")
	long := graphFixtureKey(fid("07"), "future_kind", strings.Repeat("é", 2048))
	full.RemoveNodes = []NodeKey{graphFixtureKey(fid("07"), NodeKindArtifact, "c"), graphFixtureKey(fid("07"), NodeKindArtifact, "d")}
	full.RemoveEdges = sortedDigests(goldenB1, goldenB2)
	full.PutNodes = []GraphNode{{one, goldenB1, AdapterPython, goldenB2}, {two, goldenB2, AdapterSQL, goldenB3}, {long, goldenB3, "future_adapter", goldenB1}}
	full.PutEdges = []GraphEdge{graphFixtureEdge(one, two, AdapterPython, "b"), graphFixtureEdge(two, long, AdapterSQL, strings.Repeat("é", 2048))}
	full.PutEdges[1].Type, full.PutEdges[1].Geometry = "future_edge", "future_geometry"
	full.PutEdges[1].EdgeID = EdgeID(two, long, "future_edge")
	full.PutEdges[1].WeightPPM = 1
	slices.SortFunc(full.PutEdges, func(a, b GraphEdge) int { return bytes.Compare(a.EdgeID[:], b.EdgeID[:]) })
	full.Coverage = []CoverageEntry{{"a", AdapterOpaque, CoverageComplete, []string{}}, {"a", AdapterPython, CoverageUnknown, []string{"future_reason", ReasonHelperLimit}}, {strings.Repeat("z", 8192), AdapterOpaque, CoverageUnknown, []string{ReasonSourceUnavailable}}}
	committed := GraphVersionCommitted{AnalysisID: start.AnalysisID, AnalysisKey: goldenB1, BaseDigest: EmptyGraphDigest, ChunksSHA256: EmptyChunksDigest, GraphDigest: EmptyGraphDigest}
	changed := committed
	changed.BaseVersion, changed.BaseDigest, changed.ChunkCount, changed.ChunksSHA256, changed.GraphDigest, changed.StructuralSinceRebuild = math.MaxInt64, goldenB1, 3, goldenB2, goldenB3, math.MaxInt64
	return []struct {
		name, typ string
		p         payload
	}{
		{"load", TypeGraphAnalysisStarted, start}, {"incremental", TypeGraphAnalysisStarted, incremental}, {"future reason", TypeGraphAnalysisStarted, future},
		{"reserved", TypeGraphAnalysisProgress, reserved}, {"checkpoint", TypeGraphAnalysisProgress, checkpoint}, {"unknown", TypeGraphAnalysisProgress, unknown}, {"superseded", TypeGraphAnalysisProgress, superseded},
		{"empty chunk", TypeGraphDeltaChunk, empty}, {"full chunk", TypeGraphDeltaChunk, full},
		{"confirmation", TypeGraphVersionCommitted, committed}, {"changed", TypeGraphVersionCommitted, changed},
	}
}

func TestU3_61PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range u3_61Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js := mustEncode(tc.p)
			// Each object, including nested keys/evidence/references, gets an extension.
			extended := bytes.ReplaceAll(js, []byte(`{`), []byte(`{"later":{"a":[1,null]},`))
			for _, data := range [][]byte{js, extended} {
				got, err := decodeU3_61(tc.typ, data)
				if err != nil || !reflect.DeepEqual(got, tc.p) {
					t.Fatalf("Decode = %+v, %v; want %+v", got, err, tc.p)
				}
				if encoded, err := got.Encode(); err != nil || !bytes.Equal(encoded, js) {
					t.Fatalf("re-encoding = %s, %v; want %s", encoded, err, js)
				}
			}
			p := reflect.New(reflect.TypeOf(tc.p)).Elem()
			p.Set(reflect.ValueOf(tc.p))
			var nilEmpty func(reflect.Value)
			nilEmpty = func(v reflect.Value) {
				switch v.Kind() {
				case reflect.Struct:
					for i := range v.NumField() {
						nilEmpty(v.Field(i))
					}
				case reflect.Slice:
					if v.Len() == 0 {
						v.SetZero()
						return
					}
					copy := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
					reflect.Copy(copy, v)
					v.Set(copy)
					for i := range v.Len() {
						nilEmpty(v.Index(i))
					}
				}
			}
			nilEmpty(p)
			if encoded := mustEncode(p.Interface().(payload)); !bytes.Equal(encoded, js) {
				t.Fatalf("nil arrays changed encoding: %s", encoded)
			}
		})
	}
	t.Logf("EmptyGraphDigest=%s EmptyChunksDigest=%s", EmptyGraphDigest, EmptyChunksDigest)
}

// graphWireAt changes one JSON value without invoking a contract decoder.
// An empty replacement removes an object member; array elements are retained.
func graphWireAt(js []byte, path []string, replacement []byte) []byte {
	if len(path) == 0 {
		return replacement
	}
	if js[0] == '{' {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(js, &m); err != nil {
			panic(err)
		}
		if len(path) == 1 && replacement == nil {
			delete(m, path[0])
		} else {
			m[path[0]] = graphWireAt(m[path[0]], path[1:], replacement)
		}
		out, err := json.Marshal(m)
		if err != nil {
			panic(err)
		}
		return out
	}
	var a []json.RawMessage
	if err := json.Unmarshal(js, &a); err != nil {
		panic(err)
	}
	i, err := strconv.Atoi(path[0])
	if err != nil {
		panic(err)
	}
	a[i] = graphWireAt(a[i], path[1:], replacement)
	out, err := json.Marshal(a)
	if err != nil {
		panic(err)
	}
	return out
}

// graphGoValue builds the Go-side analogue independently of the codecs, so
// a shared Encode/Decode validation omission cannot make refusal tests pass.
func graphGoValue(raw []byte, v reflect.Value) error {
	if v.Type() == reflect.TypeFor[Digest]() {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		b, err := hex.DecodeString(s)
		if err != nil || len(b) != 32 {
			return errors.New("not a digest")
		}
		v.Set(reflect.ValueOf(Digest(b)))
		return nil
	}
	if v.Type() == reflect.TypeFor[uuid.UUID]() {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(id))
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		for i := range v.NumField() {
			if r, ok := m[v.Type().Field(i).Tag.Get("json")]; ok {
				if err := graphGoValue(r, v.Field(i)); err != nil {
					return err
				}
			}
		}
		return nil
	case reflect.Slice:
		var a []json.RawMessage
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		v.Set(reflect.MakeSlice(v.Type(), len(a), len(a)))
		for i, r := range a {
			if err := graphGoValue(r, v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return json.Unmarshal(raw, v.Addr().Interface())
	}
}

func TestU3_61PayloadsRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range u3_61Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js := mustEncode(tc.p)
			refuse := func(name string, bad []byte, goSide bool) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if _, err := decodeU3_61(tc.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Errorf("Decode = %v; want ErrInvalid", err)
					}
					if goSide {
						v := reflect.New(reflect.TypeOf(tc.p)).Elem()
						if err := graphGoValue(bad, v); err != nil {
							t.Fatalf("Go analogue: %v", err)
						}
						if out, err := v.Interface().(payload).Encode(); out != nil || !errors.Is(err, ErrInvalid) {
							t.Errorf("Encode = %s, %v; want nil, ErrInvalid", out, err)
						}
					}
				})
			}
			change := func(path, value string, goSide bool) {
				name := path + "=" + value
				if len(name) > 128 {
					name = fmt.Sprintf("%s=%d bytes", path, len(value))
				}
				refuse(name, graphWireAt(js, strings.Split(path, "/"), []byte(value)), goSide)
			}
			var walk func([]byte, []string)
			walk = func(raw []byte, path []string) {
				if raw[0] == '{' {
					var m map[string]json.RawMessage
					if err := json.Unmarshal(raw, &m); err != nil {
						t.Fatal(err)
					}
					for key, value := range m {
						p := append(slices.Clone(path), key)
						name := strings.Join(p, "/")
						refuse("missing "+name, graphWireAt(js, p, nil), false)
						refuse("null "+name, graphWireAt(js, p, []byte("null")), false)
						duplicate := append([]byte(`{"`+key+`":`+string(value)+`,`), raw[1:]...)
						refuse("repeated "+name, graphWireAt(js, path, duplicate), false)
						walk(value, p)
					}
					return
				}
				if raw[0] == '[' {
					var a []json.RawMessage
					if err := json.Unmarshal(raw, &a); err != nil {
						t.Fatal(err)
					}
					for i, el := range a {
						walk(el, append(slices.Clone(path), strconv.Itoa(i)))
					}
					for _, value := range []string{`{}`, `1`, `[null]`} {
						refuse("array shape "+strings.Join(path, "/")+value, graphWireAt(js, path, []byte(value)), false)
					}
					if len(a) > 0 {
						dup := []byte("[" + string(a[0]) + "," + string(a[0]) + "]")
						refuse("duplicate array "+strings.Join(path, "/"), graphWireAt(js, path, dup), true)
					}
					if len(a) > 1 {
						slices.Reverse(a)
						reversed, _ := json.Marshal(a)
						refuse("descending array "+strings.Join(path, "/"), graphWireAt(js, path, reversed), true)
					}
					return
				}
				key := path[len(path)-1]
				name := strings.Join(path, "/")
				if raw[0] != '"' {
					for _, value := range []string{`-0`, `1.0`, `1e3`, `"1"`, `-1`, `9223372036854775808`} {
						refuse("integer "+name+"="+value, graphWireAt(js, path, []byte(value)), value == "-1")
					}
					if key == "seq" || key == "analyzer_epoch" || key == "revision" || key == "weight_ppm" {
						change(name, "0", true)
					}
					if key == "weight_ppm" {
						change(name, "1000001", true)
					}
					return
				}
				var values []string
				switch {
				case key == "edge_id" || key == "hash" || key == "analysis_key" || strings.HasSuffix(key, "digest") || strings.HasSuffix(key, "sha256") || len(path) > 1 && path[len(path)-2] == "remove_edges":
					values = []string{strings.Repeat("0", 64)}
					for _, value := range []string{`"` + strings.Repeat("A", 64) + `"`, `"abc"`, `1`} {
						refuse("digest shape "+name+value, graphWireAt(js, path, []byte(value)), false)
					}
				case strings.HasSuffix(key, "_id"):
					values = []string{"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f", "00000000-0000-0000-0000-000000000000"}
				case key == "tree":
					values = []string{strings.Repeat("a", 39), strings.Repeat("a", 65), strings.Repeat("A", 40)}
				case key == "limits_profile" || key == "helper_profile":
					values = []string{"engine-limits/v1", "", "a/0", strings.Repeat("a", 63) + "/1"}
				case key == "local_key" || key == "reference":
					values = []string{"", strings.Repeat("x", 4097)}
				case key == "scope" && tc.typ != TypeGraphAnalysisStarted:
					values = []string{"", strings.Repeat("x", 8193)}
				case key == "state":
					values = []string{"future_state"}
				case key == "phase":
					values = []string{"future_phase"}
				default:
					values = []string{"", "A", "a.b", "a-b", "0a", strings.Repeat("a", 65)}
				}
				for i, value := range values {
					quoted, _ := json.Marshal(value)
					refuse(fmt.Sprintf("value %s %d", name, i), graphWireAt(js, path, quoted), true)
				}
				if key == "local_key" || key == "reference" || key == "scope" && tc.typ != TypeGraphAnalysisStarted {
					// Invalid UTF-8 must not be repaired silently on either path.
					bad := bytes.Replace(js, raw, []byte{'"', 0xff, '"'}, 1)
					refuse("UTF-8 "+name, bad, false)
					v := reflect.New(reflect.TypeOf(tc.p)).Elem()
					if err := graphGoValue(js, v); err != nil {
						t.Fatal(err)
					}
					field := v
					for _, part := range path {
						if field.Kind() == reflect.Slice {
							i, _ := strconv.Atoi(part)
							field = field.Index(i)
						} else {
							for i := range field.NumField() {
								if field.Type().Field(i).Tag.Get("json") == part {
									field = field.Field(i)
									break
								}
							}
						}
					}
					field.SetString(string([]byte{0xff}))
					if _, err := v.Interface().(payload).Encode(); !errors.Is(err, ErrInvalid) {
						t.Errorf("Encode invalid UTF-8 %s: %v", name, err)
					}
				}
			}
			walk(js, nil)
			refuse("not object", []byte(`[]`), false)
			refuse("repeated unknown", append([]byte(`{"future":{"a":1,"a":2},`), js[1:]...), false)
			switch p := tc.p.(type) {
			case GraphAnalysisStarted:
				change("source/registered/seq", strconv.FormatInt(p.Source.Subject.Seq, 10), true)
				if p.Source.Subject.Seq < math.MaxInt64 {
					change("source/registered/seq", strconv.FormatInt(p.Source.Subject.Seq+1, 10), true)
				}
				change("source/subject/seq", "1", true)
				change("input_frontier/seq", "1", true)
				if p.BaseVersion == 0 {
					change("base_digest", `"`+goldenB1.String()+`"`, true)
				} else {
					change("base_digest", `"`+EmptyGraphDigest.String()+`"`, true)
				}
			case GraphAnalysisProgress:
				if p.Phase == GraphPhaseUnknown || p.Phase == GraphPhaseSuperseded {
					change("coverage", "[]", true)
					bad := graphWireAt(js, []string{"coverage", "0", "state"}, []byte(`"complete"`))
					bad = graphWireAt(bad, []string{"coverage", "0", "reasons"}, []byte(`[]`))
					refuse("terminal complete", bad, true)
				}
			case GraphDeltaChunk:
				if len(p.PutNodes) > 0 {
					key, _ := json.Marshal([]NodeKey{p.PutNodes[0].Key})
					change("remove_nodes", string(key), true)
					edge, _ := json.Marshal([]Digest{p.PutEdges[0].EdgeID})
					change("remove_edges", string(edge), true)
					same := p.PutEdges[0]
					same.To = same.From
					same.EdgeID = EdgeID(same.From, same.To, same.Type)
					edge, _ = json.Marshal([]GraphEdge{same})
					change("put_edges", string(edge), true)
					change("put_edges/0/edge_id", `"`+goldenB3.String()+`"`, true)
					for _, typ := range []string{"", "A", "a.b", "a-b", "0a", strings.Repeat("a", 65)} {
						badEdge := p.PutEdges[0]
						badEdge.Type = typ
						badEdge.EdgeID = EdgeID(badEdge.From, badEdge.To, typ)
						encoded, _ := json.Marshal([]GraphEdge{badEdge})
						change("put_edges", string(encoded), true)
					}
					change("coverage/0/reasons", `["helper_limit"]`, true)
					change("coverage/1/reasons", `[]`, true)
					descendingScopes, _ := json.Marshal([]CoverageEntry{p.Coverage[2], p.Coverage[0]})
					change("coverage", string(descendingScopes), true)
				}
			case GraphVersionCommitted:
				if p.BaseVersion == 0 {
					change("base_digest", `"`+goldenB1.String()+`"`, true)
				} else {
					change("base_version", "0", true)
					change("base_digest", `"`+EmptyGraphDigest.String()+`"`, true)
				}
				if p.ChunkCount == 0 {
					change("chunks_sha256", `"`+goldenB1.String()+`"`, true)
					change("graph_digest", `"`+goldenB1.String()+`"`, true)
				} else {
					change("chunks_sha256", `"`+EmptyChunksDigest.String()+`"`, true)
					change("graph_digest", `"`+p.BaseDigest.String()+`"`, true)
				}
			}
		})
	}
}

func TestEdgeID(t *testing.T) {
	t.Parallel()
	from := graphFixtureKey(fid("07"), NodeKindModule, `["python","workspace","pkg.a"]`)
	to := graphFixtureKey(fid("07"), NodeKindArtifact, `[9,"pkg/b.py"]`)
	literal := `[{"namespace":"workspace","origin_id":"0199c000-0000-7000-8000-000000000007","kind":"module","local_key":"[\"python\",\"workspace\",\"pkg.a\"]"},{"namespace":"workspace","origin_id":"0199c000-0000-7000-8000-000000000007","kind":"artifact","local_key":"[9,\"pkg/b.py\"]"},"static_dependency"]`
	want := Digest(sha256.Sum256([]byte(literal)))
	if got := EdgeID(from, to, "static_dependency"); got != want {
		t.Fatalf("EdgeID=%s want %s", got, want)
	}
	if EdgeID(to, from, "static_dependency") == want || EdgeID(from, to, "future_type") == want {
		t.Fatal("edge identity ignored direction or type")
	}
	edge := graphFixtureEdge(from, to, AdapterPython, "pkg.a")
	edge.Evidence.Reference = "replacement"
	edge.Evidence.ArtifactSHA256 = goldenB3
	js := mustEncode(GraphDeltaChunk{AnalysisID: fid("18"), AnalysisKey: goldenB1, PutEdges: []GraphEdge{edge}})
	p, err := DecodeGraphDeltaChunk(js)
	if err != nil || p.PutEdges[0].EdgeID != want {
		t.Fatalf("replacement evidence changed edge identity: %+v, %v", p, err)
	}
}
