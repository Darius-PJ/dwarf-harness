package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"uuid"
)

func validationConfigFixture() (WorkspaceChecks, EnvironmentPolicy, FileManifest) {
	d := Digest(sha256.Sum256([]byte("validation fixture")))
	w := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	files := []ValidationFile{{Path: "go.mod", Mode: "100644", Bytes: 0, SHA256: d}}
	check := CheckSpec{
		CheckID: "unit", Adapter: "go-test-json", AdapterVersion: 1,
		ExecutableID: "go", Package: ".", WorkingDirectory: ".", DefiningFiles: files,
		Inventory:     []ValidationInventoryNode{{Package: "example.test/unit", Name: "TestUnit", Expected: "passed"}},
		Canary:        ValidationCanarySpec{Protocol: "go-fail-v1", PackageName: "unit", File: "dwarf_canary_v1_test.go", TestName: "TestDwarfCanaryV1", TemplateKind: "testing-error-v1", TemplateSHA256: d},
		EnvironmentID: "go", NetworkGrantRefs: []uuid.UUID{}, SecretGrantRefs: []uuid.UUID{},
		WallMS: 1000, OutputBytes: 4096, Processes: 4,
	}
	systemRoot := "C:/fixture/Windows"
	environment := EnvironmentSpec{
		EnvironmentID: "go", OS: "windows", Arch: "amd64", ExecutableID: "go",
		ExecutablePath: "C:/toolchain/bin/go.exe", ExecutableSHA256: d,
		ToolchainVersion: "go1.27.4", ToolchainRoot: "C:/toolchain",
		ToolchainManifestSHA256: d, DependencyManifestSHA256: d,
		LockFiles: files, Variables: []ValidationVariable{{Name: "SystemRoot", Value: &systemRoot}}, GOMAXPROCS: 1, MemoryReservationBytes: 1048576,
	}
	return WorkspaceChecks{WorkspaceChecksFormat, 1, w, 1, []string{"unit"}, []CheckSpec{check}, []ValidationHook{}},
		EnvironmentPolicy{EnvironmentPolicyFormat, 1, w, 1, []EnvironmentSpec{environment}},
		FileManifest{FileManifestFormat, 1, files}
}

func TestValidationConfigurationRoundTrip(t *testing.T) {
	t.Parallel()
	checks, environment, manifest := validationConfigFixture()
	js, err := checks.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, digest, err := LoadWorkspaceChecks(js)
	if err != nil {
		t.Fatal(err)
	}
	back, err := got.Encode()
	if err != nil || !bytes.Equal(back, js) || digest != Digest(sha256.Sum256(js)) {
		t.Fatalf("workspace checks round-trip: %v", err)
	}
	js, err = environment.Encode()
	if err != nil {
		t.Fatal(err)
	}
	env, digest, err := LoadEnvironmentPolicy(js)
	if err != nil {
		t.Fatal(err)
	}
	back, err = env.Encode()
	if err != nil || !bytes.Equal(back, js) || digest != Digest(sha256.Sum256(js)) {
		t.Fatalf("environment round-trip: %v", err)
	}
	js, err = manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	files := []ValidationFile{}
	digest, err = LoadFileManifest(bytes.NewReader(js), 10, func(f ValidationFile) error { files = append(files, f); return nil })
	if err != nil {
		t.Fatal(err)
	}
	back, err = (FileManifest{FileManifestFormat, 1, files}).Encode()
	if err != nil || !bytes.Equal(back, js) || digest != Digest(sha256.Sum256(js)) {
		t.Fatalf("manifest round-trip: %v", err)
	}
	for _, principal := range []string{"owner", "hook"} {
		data, err := os.ReadFile("testdata/f4/17/authorization/" + principal + ".json")
		if err != nil {
			t.Fatal(err)
		}
		document, digest, err := LoadValidationAuthorization(data)
		if err != nil {
			t.Fatal(err)
		}
		back, err := document.Encode()
		if err != nil || !bytes.Equal(back, data) || digest != Digest(sha256.Sum256(data)) || document.Principal != principal {
			t.Fatalf("%s authorization round-trip: %v", principal, err)
		}
	}
}

func TestValidationConfigurationRefusals(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"empty_required", "missing_check", "unknown_member", "unsupported_adapter", "unsorted_manifest"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checks, _, manifest := validationConfigFixture()
			var err error
			switch name {
			case "empty_required":
				checks.Required = []string{}
			case "missing_check":
				checks.Required = []string{"missing"}
			case "unsupported_adapter":
				checks.Checks[0].Adapter = "future-adapter"
			case "unsorted_manifest":
				manifest.Files = append(manifest.Files, manifest.Files[0])
				js, marshalErr := json.Marshal(manifest)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				_, err = LoadFileManifest(bytes.NewReader(js), 10, func(ValidationFile) error { return nil })
			default:
			}
			if name != "unsorted_manifest" {
				js, marshalErr := json.Marshal(checks)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if name == "unknown_member" {
					js = append([]byte(`{"waiver":true,`), js[1:]...)
				}
				_, _, err = LoadWorkspaceChecks(js)
			}
			if name == "unsupported_adapter" {
				if !errors.Is(err, ErrUnsupportedValidation) || errors.Is(err, ErrInvalid) {
					t.Fatalf("unsupported adapter error: %v", err)
				}
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatalf("refusal error: %v", err)
			}
		})
	}
}
