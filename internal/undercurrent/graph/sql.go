package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type sqlToken struct {
	value      string
	quoted     bool
	start, end int
}
type sqlStatement struct {
	table, reference string
	create           bool
}

var errSQLSyntax = errors.New("unsupported SQL syntax")
var errSQLWork = errors.New("SQL work exhausted")

// AnalyzeSQL recognizes only literal CREATE TABLE and SELECT ... FROM. It
// tokenizes complete files before resolving tables, so comments, strings and
// partially valid syntax never manufacture edges. No database is contacted.
func AnalyzeSQL(ctx context.Context, r AnalysisRequest) (AnalysisResult, error) {
	if err := validateRequest(r); err != nil {
		if errors.Is(err, errCorpusLimit) {
			return unknown(r, ledger.AdapterSQL, ledger.ReasonHelperLimit, r.Allowance), nil
		}
		return AnalysisResult{}, err
	}
	out := AnalysisResult{Nodes: []ledger.GraphNode{}, Edges: []ledger.GraphEdge{}, Coverage: []ledger.CoverageEntry{}}
	charge := func(n int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n > r.Allowance-out.WorkUsed {
			return errSQLWork
		}
		out.WorkUsed += n
		return nil
	}
	parsed := make(map[int][]sqlStatement)
	reasons := make(map[int]string)
	decls := make(map[string][]int)
	for i, a := range r.Corpus {
		if strings.ToLower(path.Ext(a.Path)) != ".sql" {
			continue
		}
		err := charge(1 + int64((len(a.Bytes)+255)/256))
		var statements []sqlStatement
		if err == nil {
			var tokens []sqlToken
			tokens, err = sqlTokens(a.Bytes, charge)
			if err == nil {
				statements, err = parseSQL(tokens, a.Bytes)
			}
		}
		if errors.Is(err, errSQLWork) {
			return unknown(r, ledger.AdapterSQL, ledger.ReasonWorkExhausted, r.Allowance), nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return AnalysisResult{}, ctx.Err()
			}
			reasons[i] = ledger.ReasonUnsupportedSyntax
			parsed[i] = nil
			continue
		}
		parsed[i] = statements
		for _, s := range statements {
			if s.create {
				decls[s.table] = append(decls[s.table], i)
			}
		}
	}
	modules := make(map[string]ledger.NodeKey)
	for i := range r.Corpus {
		for _, s := range parsed[i] {
			if !s.create {
				continue
			}
			indices := decls[s.table]
			if len(indices) != 1 {
				for _, j := range indices {
					reasons[j] = ledger.ReasonUnresolvedReference
				}
				continue
			}
			a := r.Corpus[i]
			k, err := ModuleKey(r.Source.WorkspaceID, ledger.AdapterSQL, "workspace", s.table)
			if err != nil {
				reasons[i] = ledger.ReasonUnsupportedSyntax
				continue
			}
			version, err := ModuleVersion(s.table, []ModuleMember{{a.Path, a.SHA256}})
			if err != nil {
				return AnalysisResult{}, err
			}
			modules[s.table] = k
			out.Nodes = append(out.Nodes, ledger.GraphNode{Key: k, VersionSHA256: version, Adapter: ledger.AdapterSQL, ConfigurationSHA256: r.ConfigurationSHA256})
		}
	}
	for i, a := range r.Corpus {
		statements, ok := parsed[i]
		if !ok {
			continue
		}
		var edges []ledger.GraphEdge
		for _, s := range statements {
			if s.create {
				continue
			}
			k, ok := modules[s.table]
			if !ok {
				reasons[i] = ledger.ReasonUnresolvedReference
				continue
			}
			e := dependency(k, a, ledger.AdapterSQL, s.reference, r.ConfigurationSHA256)
			if !edgeValid(e) {
				reasons[i] = ledger.ReasonUnsupportedSyntax
				continue
			}
			edges = append(edges, e)
		}
		if reasons[i] == "" {
			out.Edges = append(out.Edges, edges...)
		}
		out.Coverage = append(out.Coverage, covered(a.Key, ledger.AdapterSQL, reasons[i]))
	}
	if err := charge(int64(len(out.Nodes) + len(out.Edges) + len(out.Coverage))); err != nil {
		if ctx.Err() != nil {
			return AnalysisResult{}, ctx.Err()
		}
		return unknown(r, ledger.AdapterSQL, ledger.ReasonWorkExhausted, r.Allowance), nil
	}
	out.NextCursor.ArtifactIndex = int64(len(r.Corpus))
	sortResult(&out)
	encoded, _ := json.Marshal(out)
	if err := charge(int64((len(encoded) + 255) / 256)); err != nil {
		return unknown(r, ledger.AdapterSQL, ledger.ReasonWorkExhausted, r.Allowance), nil
	}
	return out, nil
}

func sqlTokens(data []byte, charge func(int64) error) ([]sqlToken, error) {
	if len(data) > maxBlob || !utf8.Valid(data) {
		return nil, errSQLSyntax
	}
	var out []sqlToken
	for i := 0; i < len(data); {
		c := data[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if i+1 < len(data) && string(data[i:i+2]) == "--" {
			if err := charge(1); err != nil {
				return nil, err
			}
			i += 2
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(data) && string(data[i:i+2]) == "/*" {
			if err := charge(1); err != nil {
				return nil, err
			}
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				return nil, errSQLSyntax
			}
			i += end + 4
			continue
		}
		if err := charge(1); err != nil {
			return nil, err
		}
		if c == '"' {
			start := i
			i++
			var b strings.Builder
			closed := false
			for i < len(data) {
				if data[i] == '"' {
					i++
					if i < len(data) && data[i] == '"' {
						b.WriteByte('"')
						i++
						continue
					}
					closed = true
					break
				}
				b.WriteByte(data[i])
				i++
			}
			if !closed || b.Len() == 0 || b.Len() > 4096 {
				return nil, errSQLSyntax
			}
			out = append(out, sqlToken{value: b.String(), quoted: true, start: start, end: i})
			continue
		}
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			start := i
			i++
			for i < len(data) {
				v := data[i]
				if !(v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || v == '_') {
					break
				}
				i++
			}
			if i-start > 4096 {
				return nil, errSQLSyntax
			}
			out = append(out, sqlToken{value: strings.ToLower(string(data[start:i])), start: start, end: i})
			continue
		}
		if strings.ContainsRune("(),;.*", rune(c)) {
			out = append(out, sqlToken{value: string(c), start: i, end: i + 1})
			i++
			continue
		}
		return nil, errSQLSyntax
	}
	return out, nil
}

func parseSQL(tokens []sqlToken, data []byte) ([]sqlStatement, error) {
	i := 0
	take := func(s string) bool {
		if i < len(tokens) && !tokens[i].quoted && tokens[i].value == s {
			i++
			return true
		}
		return false
	}
	ident := func() (string, bool) {
		if i >= len(tokens) {
			return "", false
		}
		t := tokens[i]
		if !t.quoted && (t.value == "" || strings.ContainsAny(t.value, "(),;.*")) {
			return "", false
		}
		i++
		return t.value, true
	}
	table := func() (string, string, bool) {
		start := i
		first, ok := ident()
		if !ok {
			return "", "", false
		}
		schema, name := "", first
		if take(".") {
			schema = first
			name, ok = ident()
			if !ok {
				return "", "", false
			}
		}
		b, _ := json.Marshal([2]string{schema, name})
		return string(b), string(data[tokens[start].start:tokens[i-1].end]), true
	}
	var out []sqlStatement
	for i < len(tokens) {
		s := sqlStatement{}
		if take("create") {
			s.create = true
			if !take("table") {
				return nil, errSQLSyntax
			}
			var ok bool
			s.table, s.reference, ok = table()
			if !ok || !take("(") {
				return nil, errSQLSyntax
			}
			for {
				if _, ok := ident(); !ok {
					return nil, errSQLSyntax
				}
				if _, ok := ident(); !ok {
					return nil, errSQLSyntax
				}
				if !take(",") {
					break
				}
			}
			if !take(")") {
				return nil, errSQLSyntax
			}
		} else if take("select") {
			for {
				if !take("*") {
					if _, ok := ident(); !ok {
						return nil, errSQLSyntax
					}
				}
				if !take(",") {
					break
				}
			}
			if !take("from") {
				return nil, errSQLSyntax
			}
			var ok bool
			s.table, s.reference, ok = table()
			if !ok {
				return nil, errSQLSyntax
			}
		} else {
			return nil, errSQLSyntax
		}
		if !take(";") {
			return nil, errSQLSyntax
		}
		out = append(out, s)
	}
	return out, nil
}
