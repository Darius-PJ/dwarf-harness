package main

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Modules outside the root module, served by local replace directives so that
// go list never needs the network.
const (
	xModule          = "golang.org/x/fake"
	thirdPartyModule = "example.com/thirdparty"
)

const fixtureGoMod = `module github.com/Darius-PJ/dwarf-harness

go 1.27

require (
	example.com/thirdparty v0.0.0
	golang.org/x/fake v0.0.0
)

replace (
	example.com/thirdparty => ../thirdparty
	golang.org/x/fake => ../xfake
)
`

// fixtureBase holds import-free packages that every fixture module contains
// and that the cases import.
var fixtureBase = map[string]string{
	"contract/ledger/ledger.go":              src("ledger"),
	"contract/ledger/appender/appender.go":   src("appender"),
	"contract/launcher/launcher.go":          src("launcher"),
	"contract/module/module.go":              src("module"),
	"internal/undercurrent/engine/engine.go": src("engine"),
	"internal/undercurrent/graph/graph.go":   src("graph"),
	"internal/router/route/route.go":         src("route"),
	"internal/core/host/host.go":             src("host"),
}

// src returns a Go file of package pkg with a blank import of each path.
func src(pkg string, imports ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n", pkg)
	for _, imp := range imports {
		fmt.Fprintf(&b, "\nimport _ %q\n", imp)
	}
	return b.String()
}

// m joins a module-relative path to the module path.
func m(rel string) string { return modulePath + "/" + rel }

func forbidden(pkg, imp string) violation {
	return violation{kind: forbiddenImport, pkg: m(pkg), subject: imp}
}

// writeFixture builds a throwaway module from fixtureBase and files, beside
// the local modules its replace directives name, and returns its directory.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "xfake", "go.mod"), "module "+xModule+"\n\ngo 1.27\n")
	writeFile(t, filepath.Join(root, "xfake", "fake.go"), src("fake"))
	writeFile(t, filepath.Join(root, "thirdparty", "go.mod"), "module "+thirdPartyModule+"\n\ngo 1.27\n")
	writeFile(t, filepath.Join(root, "thirdparty", "thirdparty.go"), src("thirdparty"))
	dir := filepath.Join(root, "main")
	writeFile(t, filepath.Join(dir, "go.mod"), fixtureGoMod)
	for name, content := range fixtureBase {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	for name, content := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	return dir
}

// boundaryOf runs the real import-boundary check on a fixture module built
// from files, without network access.
func boundaryOf(t *testing.T, files map[string]string) []finding {
	t.Helper()
	var log strings.Builder
	found, err := boundaryFindings(&log, writeFixture(t, files), "GOPROXY=off", "GOFLAGS=-mod=mod", "GOWORK=off")
	if err != nil {
		t.Fatalf("boundary check: %v\n%s", err, log.String())
	}
	return found
}

// TestBoundaryIgnoresAmbientGOFLAGS plants a forbidden import behind a build
// tag and sets that tag through GOFLAGS in the environment. The boundary step
// must not see the file, because its go list clears GOFLAGS.
func TestBoundaryIgnoresAmbientGOFLAGS(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"internal/undercurrent/engine/plant.go": "//go:build planted\n\n" + src("engine", m("internal/router/route")),
	})
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-tags=planted")

	// Control: with the tag passed explicitly, the planted file is a violation.
	var log strings.Builder
	found, err := boundaryFindings(&log, dir, "GOFLAGS=-tags=planted")
	want := forbidden("internal/undercurrent/engine", m("internal/router/route"))
	if err != nil || len(found) != 1 || found[0].violation != want || len(found[0].targets) != len(targets) {
		t.Fatalf("control run: found %v, %v; want %v on every target\n%s", found, err, want, log.String())
	}

	log.Reset()
	if err := checkBoundary(&log, dir); err != nil {
		t.Errorf("GOFLAGS=-tags=planted in the environment reached the boundary step: %v\n%s", err, log.String())
	}
}

func TestImportBoundary(t *testing.T) {
	var all []string
	for _, tg := range targets {
		all = append(all, tg.String())
	}
	cgoSource := "package host\n\nimport \"C\"\n"

	tests := []struct {
		name  string
		files map[string]string
		want  []violation
		// on lists the targets where want appears; nil means every target.
		on []string
	}{
		// Must pass.
		{name: "contract imports only the standard library", files: map[string]string{
			"contract/ledger/plant.go": src("ledger", "io"),
		}},
		{name: "same-tree import within contract", files: map[string]string{
			"contract/launcher/plant.go": src("launcher", m("contract/ledger")),
		}},
		{name: "undercurrent imports contract", files: map[string]string{
			"internal/undercurrent/engine/plant.go": src("engine", m("contract/ledger"), m("contract/module")),
		}},
		{name: "router imports contract and golang.org/x", files: map[string]string{
			"internal/router/route/plant.go": src("route", m("contract/module"), xModule),
		}},
		{name: "core imports contract router undercurrent and golang.org/x", files: map[string]string{
			"internal/core/host/plant.go": src("host", m("contract/ledger"), m("internal/router/route"),
				m("internal/undercurrent/engine"), xModule),
		}},
		{name: "cmd and its tests import core and contract", files: map[string]string{
			"cmd/dwarf-harness/main.go":               src("main", m("internal/core/host"), m("contract/launcher")),
			"cmd/dwarf-harness/main_internal_test.go": src("main", "testing", m("contract/ledger")),
			"cmd/dwarf-harness/main_external_test.go": src("main_test", "testing", m("cmd/dwarf-harness"), m("internal/core/host")),
		}},
		{name: "tools import golang.org/x", files: map[string]string{
			"tools/check/main.go": src("main", xModule),
		}},
		{name: "gui imports contract", files: map[string]string{
			"gui/view/view.go": src("view", m("contract/launcher")),
		}},
		{name: "test variants import their package under test", files: map[string]string{
			"internal/core/host/host_internal_test.go": src("host", "testing", m("contract/ledger")),
			"internal/core/host/host_external_test.go": src("host_test", "testing", m("internal/core/host")),
			"contract/ledger/ledger_external_test.go":  src("ledger_test", "testing", m("contract/ledger")),
		}},
		{name: "appender imported by graph core router and its own test", files: map[string]string{
			"internal/undercurrent/graph/plant.go":      src("graph", m("contract/ledger/appender")),
			"internal/core/host/plant.go":               src("host", m("contract/ledger/appender")),
			"internal/router/route/plant.go":            src("route", m("contract/ledger/appender")),
			"contract/ledger/appender/appender_test.go": src("appender_test", "testing", m("contract/ledger/appender")),
		}},
		{name: "allowed imports in OS-specific files", files: map[string]string{
			"internal/core/host/plant_linux.go":    src("host", xModule),
			"internal/core/host/plant_darwin.go":   src("host", m("internal/router/route")),
			"internal/router/route/plant_linux.go": src("route", m("contract/ledger")),
		}},

		// Must fail.
		{
			name:  "undercurrent imports router",
			files: map[string]string{"internal/undercurrent/engine/plant.go": src("engine", m("internal/router/route"))},
			want:  []violation{forbidden("internal/undercurrent/engine", m("internal/router/route"))},
		},
		{
			name:  "contract imports core",
			files: map[string]string{"contract/ledger/plant.go": src("ledger", m("internal/core/host"))},
			want:  []violation{forbidden("contract/ledger", m("internal/core/host"))},
		},
		{
			name:  "non-graph undercurrent imports the appender",
			files: map[string]string{"internal/undercurrent/engine/plant.go": src("engine", m("contract/ledger/appender"))},
			want:  []violation{forbidden("internal/undercurrent/engine", m("contract/ledger/appender"))},
		},
		{
			name:  "cmd imports router",
			files: map[string]string{"cmd/dwarf-harness/main.go": src("main", m("internal/router/route"))},
			want:  []violation{forbidden("cmd/dwarf-harness", m("internal/router/route"))},
		},
		{
			name:  "tools import core",
			files: map[string]string{"tools/check/main.go": src("main", m("internal/core/host"))},
			want:  []violation{forbidden("tools/check", m("internal/core/host"))},
		},
		{
			name:  "core imports an unrecorded third-party module",
			files: map[string]string{"internal/core/host/plant.go": src("host", thirdPartyModule)},
			want:  []violation{forbidden("internal/core/host", thirdPartyModule)},
		},
		{
			name: "test files import an unrecorded third-party module",
			files: map[string]string{
				"internal/core/host/host_internal_test.go": src("host", "testing", thirdPartyModule),
				"internal/core/host/host_external_test.go": src("host_test", "testing", thirdPartyModule),
			},
			want: []violation{
				forbidden("internal/core/host", thirdPartyModule),
				forbidden("internal/core/host_test", thirdPartyModule),
			},
		},
		{
			name:  "undercurrent imports golang.org/x",
			files: map[string]string{"internal/undercurrent/engine/plant.go": src("engine", xModule)},
			want:  []violation{forbidden("internal/undercurrent/engine", xModule)},
		},
		{
			name:  "contract imports golang.org/x",
			files: map[string]string{"contract/ledger/plant.go": src("ledger", xModule)},
			want:  []violation{forbidden("contract/ledger", xModule)},
		},
		{
			name:  "cmd imports golang.org/x",
			files: map[string]string{"cmd/dwarf-harness/main.go": src("main", xModule)},
			want:  []violation{forbidden("cmd/dwarf-harness", xModule)},
		},
		{
			name:  "package in no tree",
			files: map[string]string{"pkg/x/x.go": src("x")},
			want:  []violation{{kind: unknownTree, pkg: m("pkg/x")}},
		},
		{
			name:  "cmd imports the appender",
			files: map[string]string{"cmd/dwarf-harness/main.go": src("main", m("contract/ledger/appender"))},
			want:  []violation{forbidden("cmd/dwarf-harness", m("contract/ledger/appender"))},
		},
		{
			name:  "contract sibling imports the appender",
			files: map[string]string{"contract/launcher/plant.go": src("launcher", m("contract/ledger/appender"))},
			want:  []violation{forbidden("contract/launcher", m("contract/ledger/appender"))},
		},
		{
			name:  "cgo file",
			files: map[string]string{"internal/core/host/plant.go": cgoSource},
			want:  []violation{{kind: cgoFile, pkg: m("internal/core/host"), subject: "plant.go"}},
		},
		{
			name:  "forbidden import in a linux-only file",
			files: map[string]string{"internal/undercurrent/engine/plant_linux.go": src("engine", m("internal/router/route"))},
			want:  []violation{forbidden("internal/undercurrent/engine", m("internal/router/route"))},
			on:    []string{"linux/amd64", "linux/arm64"},
		},
		{
			name:  "cgo in a darwin-only file",
			files: map[string]string{"internal/core/host/plant_darwin.go": cgoSource},
			want:  []violation{{kind: cgoFile, pkg: m("internal/core/host"), subject: "plant_darwin.go"}},
			on:    []string{"darwin/amd64", "darwin/arm64"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			on := tt.on
			if on == nil {
				on = all
			}
			want := map[violation][]string{}
			for _, v := range tt.want {
				want[v] = on
			}
			got := map[violation][]string{}
			for _, f := range boundaryOf(t, tt.files) {
				got[f.violation] = f.targets
			}
			if !maps.EqualFunc(got, want, slices.Equal) {
				t.Errorf("violations:\n%s\nwant:\n%s", describe(got), describe(want))
			}
		})
	}
}

func describe(vs map[violation][]string) string {
	if len(vs) == 0 {
		return "  (none)"
	}
	var lines []string
	for v, on := range vs {
		lines = append(lines, "  "+finding{v, on}.String())
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}
