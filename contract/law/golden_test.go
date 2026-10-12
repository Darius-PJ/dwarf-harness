package law

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Independently computed with PowerShell Get-FileHash over the fixture bytes.
var goldenSHA256 = map[string]string{
	"law/v1/full.json":                                    "3b148a28c5cd88445c3cd43b53e25cb99b8e9009e57ab7416e7293469134895c",
	"law/v1/full-compact.json":                            "f1382c84a68f79ea258040f160fdd4d8a55decf4bed93d3a20eb861c81965583",
	"law/v1/full-reordered.json":                          "3617c086c1cd5d2479175cbdbeb6d6fbcec0a48ca7333cae86fea730ec49b7a4",
	"law/v1/minimal.json":                                 "b6341867c260f09f666ead95102d405ad67ed2e7f38595a5c254a9fd68daf280",
	"law/v1/bounds.json":                                  "be2d34fa8e9c7fd58a133c1c9768cef4947988559ab65049fc8516b3ddb3fc43",
	"law/unsupported/schema-version-2.json":               "fa30e702051ef0695ca0501c55965a3b2beea32a9cafb6adf846d31f748232a6",
	"law/unsupported/evaluator-version-2.json":            "ecc378673dd0830be3841023cfcc47560003165a1b6bbc8b7a6cd6354eca0337",
	"law/unsupported/kernel-v2.json":                      "ba5d5a61a8b0ca6b65aac1943294c4d63bc5bca866ebc97b75471d52766a6450",
	"validation-policy/v1/policy.json":                    "1f3c66a9eca49f82e0e224c8f7a5edaee890757b6281a035f2e9a1f543f4f447",
	"validation-policy/unsupported/schema-version-2.json": "5f95d48c1dc8ac933d79d156e289e8ee0931e887d28f1e1dd2ba90e1a739402a",
}

// Get-FileHash over a MemoryStream containing full.json with LF replaced by CRLF.
const crlfSHA256 = "83cad664b5eb46a5ef8453f4d57cfce5c9023d24bd54e8d9a463c0f2b6eaf9bf"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func pinnedDigest(t testing.TB, value string) Digest {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 {
		t.Fatalf("invalid pinned digest %q: %v", value, err)
	}
	return Digest(raw)
}

func profiles() []struct {
	name   string
	limits LoadLimits
} {
	return []struct {
		name   string
		limits LoadLimits
	}{{"compact", CompactV1()}, {"standard", StandardV1()}}
}

func TestGoldenFilesPinned(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for _, dir := range []string{"law", "validation-policy"} {
		err := fs.WalkDir(os.DirFS("testdata"), dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			seen[path] = true
			want, ok := goldenSHA256[path]
			if !ok {
				t.Errorf("unrecorded golden: %s", path)
				return nil
			}
			if got := Digest(sha256.Sum256(fixture(t, path))).String(); got != want {
				t.Errorf("%s digest = %s, want %s", path, got, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for path := range goldenSHA256 {
		if !seen[path] {
			t.Errorf("missing golden: %s", path)
		}
	}
}

// Test values, not an owner-approved law. This independent table transcribes
// §13.4's candidate values; it never consults the implementation's registry.
func expectedFull() Law {
	downstream := Coupling{DependencyDownstreamV1, DependencyToConsumer, []EdgeType{DeclaredDataflow, StaticDependency}}
	local := Coupling{LocalV1, DirectionLocal, []EdgeType{}}
	overlap := Coupling{OverlapRelevanceV1, DependencyToConsumer, []EdgeType{DeclaredDataflow, StaticDependency}}
	resource := Coupling{RegisteredResourceOutwardV1, ResourceToConsumer, []EdgeType{RegisteredResourceUse}}
	return Law{
		SchemaVersion: 1, EvaluatorVersion: 1, LawID: "golden.full", LawVersion: 1, Kernel: BoundedBestArrivalV1, WeightScalePPM: 1000000,
		Kinds: []KindLaw{
			{Affordance, Evidential, AffordanceUnits, []SourceLaw{
				{RepairR2ImpulseV1, Impulse, AffordanceUnits, 86400, AgeRuleNone, 0, downstream},
				{RepairR3ImpulseV1, Impulse, AffordanceUnits, 86400, AgeRuleNone, 0, downstream},
				{RepairR3SustainedV1, Sustained, AffordanceUnits, 0, AgeRuleNone, 0, downstream},
			}, 0, 800000, 1000, 1000000},
			{DependencyStrain, Evidential, StrainUnits, []SourceLaw{
				{ValidationF2ImpulseV1, Impulse, StrainUnits, 86400, AgeRuleNone, 0, local},
				{ValidationF2SustainedV1, Sustained, StrainUnits, 0, AgeRuleNone, 0, local},
				{ValidationF3AttributedV1, Sustained, StrainUnits, 0, AgeRuleNone, 0, downstream},
			}, 0, 800000, 1000, 1000000},
			{EpistemicUncertainty, Evidential, UncertaintyUnits, []SourceLaw{
				{BaseStalenessV1, Rising, UncertaintyUnits, 0, LinearPerDayV1, 20000, local},
				{CheckFlakinessV1, Level, UncertaintyUnits, 0, AgeRuleNone, 0, local},
				{CheckQuarantineV1, Level, UncertaintyUnits, 0, AgeRuleNone, 0, local},
				{CheckRetirementV1, Level, UncertaintyUnits, 0, AgeRuleNone, 0, local},
				{PrivateOverlapV1, Level, UncertaintyUnits, 0, AgeRuleNone, 0, local},
				{PromotionOverlapV1, Impulse, UncertaintyUnits, 86400, AgeRuleNone, 0, overlap},
			}, 0, 800000, 1000, 1000000},
			{Perturbation, Extensive, PerturbationUnits, []SourceLaw{
				{CanonicalChangeV1, Impulse, PerturbationUnits, 86400, AgeRuleNone, 0, downstream},
				{FederatedPerturbationV1, Impulse, PerturbationUnits, 86400, AgeRuleNone, 0, downstream},
				{ProviderStatusV1, Level, PerturbationUnits, 0, AgeRuleNone, 0, downstream},
			}, 0, 800000, 1000, 1000000},
			{ResourcePressure, Extensive, PressureUnits, []SourceLaw{
				{CoreBudgetExposureV1, Level, PressureUnits, 0, AgeRuleNone, 0, resource},
				{CoreResourceUsageV1, Level, PressureUnits, 0, AgeRuleNone, 0, resource},
				{CoreRunnerQueueV1, Level, PressureUnits, 0, AgeRuleNone, 0, resource},
				{RouterHeadroomV1, Level, PressureUnits, 0, AgeRuleNone, 0, resource},
			}, 0, 800000, 1000, 1000000},
			{Surprise, Evidential, SurpriseUnits, []SourceLaw{
				{CheckDurationAnomalyV1, Impulse, SurpriseUnits, 3600, AgeRuleNone, 0, local},
				{CheckOutcomeAnomalyV1, Impulse, SurpriseUnits, 3600, AgeRuleNone, 0, local},
				{SharedResourcePressureAnomalyV1, Impulse, SurpriseUnits, 3600, AgeRuleNone, 0, local},
			}, 0, 800000, 1000, 1000000},
		},
	}
}

func expectedMinimal() Law {
	law := expectedFull()
	law.LawID = "m"
	for i, source := range []int{2, 0, 0, 2, 3, 1} {
		k := &law.Kinds[i]
		k.Sources = []SourceLaw{k.Sources[source]}
		k.AttenuationPPM, k.CutoffUnits, k.SaturationUnits = 1, 1, 2
		if k.Sources[0].Mode == Impulse {
			k.Sources[0].HalfLifeSeconds = 1
		}
		if k.Sources[0].Mode == Rising {
			k.Sources[0].RiseUnitsPerDay = 1
		}
	}
	law.Kinds[5].QuantityClass = Extensive
	return law
}

func expectedBounds() Law {
	law := expectedFull()
	law.LawID, law.LawVersion = "a.b_c-d"+strings.Repeat("0", 57), 2147483647
	for i, source := range []int{0, 2, 0, 1, 2, 2} {
		k := &law.Kinds[i]
		k.Sources = []SourceLaw{k.Sources[source]}
		k.HopLatencyMS, k.AttenuationPPM, k.CutoffUnits, k.SaturationUnits = 86400000, 999999, 999999999999, 1000000000000
		if k.Sources[0].Mode == Impulse {
			k.Sources[0].HalfLifeSeconds = 31536000
		}
		if k.Sources[0].Mode == Rising {
			k.Sources[0].RiseUnitsPerDay = 1000000000000
		}
	}
	return law
}

func TestGoldenLawsLoad(t *testing.T) {
	for _, tc := range []struct {
		name string
		want func() Law
	}{
		{"full", expectedFull}, {"full-compact", expectedFull}, {"full-reordered", expectedFull}, {"minimal", expectedMinimal}, {"bounds", expectedBounds},
	} {
		path := "law/v1/" + tc.name + ".json"
		for _, p := range profiles() {
			data := fixture(t, path)
			t.Run(tc.name+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				got, err := Load(data, p.limits)
				if err != nil {
					t.Fatal(err)
				}
				want := tc.want()
				want.SHA256 = pinnedDigest(t, goldenSHA256[path])
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("loaded value differs\ngot: %#v\nwant: %#v", got, want)
				}
			})
		}
	}
}

func TestGoldenUnsupported(t *testing.T) {
	for _, tc := range []struct{ path, field, value, supported string }{
		{"law/unsupported/schema-version-2.json", "schema_version", "2", "1"},
		{"law/unsupported/evaluator-version-2.json", "evaluator_version", "2", "1"},
		{"law/unsupported/kernel-v2.json", "kernel", "bounded_best_arrival/v2", "bounded_best_arrival/v1"},
		{"validation-policy/unsupported/schema-version-2.json", "schema_version", "2", "1"},
	} {
		for _, p := range profiles() {
			data := fixture(t, tc.path)
			t.Run(tc.path+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				var err error
				if strings.HasPrefix(tc.path, "law/") {
					var got Law
					got, err = Load(data, p.limits)
					if !reflect.DeepEqual(got, Law{}) {
						t.Fatal("partial law on rejection")
					}
				} else {
					var got ValidationPolicy
					got, err = LoadValidationPolicy(data, p.limits)
					if got != (ValidationPolicy{}) {
						t.Fatal("partial policy on rejection")
					}
				}
				var e *UnsupportedError
				if !errors.Is(err, ErrUnsupported) || !errors.As(err, &e) {
					t.Fatalf("got %T %v, want UnsupportedError", err, err)
				}
				if e.Field != tc.field || e.Value != tc.value || e.Supported != tc.supported {
					t.Fatalf("unsupported detail: %+v", e)
				}
			})
		}
	}
}
