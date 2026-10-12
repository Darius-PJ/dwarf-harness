package law

import (
	"crypto/sha256"
	"encoding/hex"
)

// Format strings distinguish the two independently loadable artifacts.
const (
	LawFormat    = "dwarf-harness/undercurrent-law"
	PolicyFormat = "dwarf-harness/validation-policy"
)

// Digest is SHA-256 over an artifact's exact content bytes, without a Git header.
type Digest [sha256.Size]byte

// String returns 64 lowercase hexadecimal digits, the manifest text form.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Law is a validated field law, not an activation or an approval.
type Law struct {
	SHA256           Digest
	SchemaVersion    uint32
	EvaluatorVersion uint32
	LawID            string
	LawVersion       uint32
	Kernel           Kernel
	WeightScalePPM   uint32
	Kinds            []KindLaw
}

// KindLaw declares a kind's unit, ordered sources and bounded numeric parameters.
type KindLaw struct {
	ID              Kind
	QuantityClass   QuantityClass
	Unit            Unit
	Sources         []SourceLaw
	HopLatencyMS    int64
	AttenuationPPM  uint32
	CutoffUnits     uint64
	SaturationUnits uint64
}

// SourceLaw selects a built-in evaluator rule; it contains no executable code.
type SourceLaw struct {
	RuleID          RuleID
	Mode            Mode
	Unit            Unit
	HalfLifeSeconds uint64
	AgeRuleID       AgeRule
	RiseUnitsPerDay uint64
	Coupling        Coupling
}

// Coupling declares the registered profile and its ordered edge types.
type Coupling struct {
	Profile   Profile
	Direction Direction
	EdgeTypes []EdgeType
}

// Vocabulary values are the wire strings. The empty string is never valid.
type (
	Kind          string
	QuantityClass string
	Unit          string
	Mode          string
	RuleID        string
	AgeRule       string
	Profile       string
	Direction     string
	EdgeType      string
	Kernel        string
)

const (
	Affordance           Kind = "affordance"
	DependencyStrain     Kind = "dependency_strain"
	EpistemicUncertainty Kind = "epistemic_uncertainty"
	Perturbation         Kind = "perturbation"
	ResourcePressure     Kind = "resource_pressure"
	Surprise             Kind = "surprise"
)
const (
	Extensive  QuantityClass = "extensive"
	Evidential QuantityClass = "evidential"
)
const (
	AffordanceUnits   Unit = "affordance_units"
	StrainUnits       Unit = "strain_units"
	UncertaintyUnits  Unit = "uncertainty_units"
	PerturbationUnits Unit = "perturbation_units"
	PressureUnits     Unit = "pressure_units"
	SurpriseUnits     Unit = "surprise_units"
)
const (
	Impulse   Mode = "impulse"
	Level     Mode = "level"
	Sustained Mode = "sustained"
	Rising    Mode = "rising"
)
const (
	AgeRuleNone    AgeRule = "none"
	LinearPerDayV1 AgeRule = "linear_per_day/v1"
)
const (
	LocalV1                     Profile = "local/v1"
	DependencyDownstreamV1      Profile = "dependency_downstream/v1"
	OverlapRelevanceV1          Profile = "overlap_relevance/v1"
	RegisteredResourceOutwardV1 Profile = "registered_resource_outward/v1"
)
const (
	DirectionLocal       Direction = "local"
	DependencyToConsumer Direction = "dependency_to_consumer"
	ResourceToConsumer   Direction = "resource_to_consumer"
)
const (
	DeclaredDataflow      EdgeType = "declared_dataflow"
	RegisteredResourceUse EdgeType = "registered_resource_use"
	StaticDependency      EdgeType = "static_dependency"
)
const BoundedBestArrivalV1 Kernel = "bounded_best_arrival/v1"
const (
	BaseStalenessV1                 RuleID = "base_staleness/v1"
	CanonicalChangeV1               RuleID = "canonical_change/v1"
	CheckDurationAnomalyV1          RuleID = "check_duration_anomaly/v1"
	CheckFlakinessV1                RuleID = "check_flakiness/v1"
	CheckOutcomeAnomalyV1           RuleID = "check_outcome_anomaly/v1"
	CheckQuarantineV1               RuleID = "check_quarantine/v1"
	CheckRetirementV1               RuleID = "check_retirement/v1"
	CoreBudgetExposureV1            RuleID = "core_budget_exposure/v1"
	CoreResourceUsageV1             RuleID = "core_resource_usage/v1"
	CoreRunnerQueueV1               RuleID = "core_runner_queue/v1"
	FederatedPerturbationV1         RuleID = "federated_perturbation/v1"
	PrivateOverlapV1                RuleID = "private_overlap/v1"
	PromotionOverlapV1              RuleID = "promotion_overlap/v1"
	ProviderStatusV1                RuleID = "provider_status/v1"
	RepairR2ImpulseV1               RuleID = "repair_r2_impulse/v1"
	RepairR3ImpulseV1               RuleID = "repair_r3_impulse/v1"
	RepairR3SustainedV1             RuleID = "repair_r3_sustained/v1"
	RouterHeadroomV1                RuleID = "router_headroom/v1"
	SharedResourcePressureAnomalyV1 RuleID = "shared_resource_pressure_anomaly/v1"
	ValidationF2ImpulseV1           RuleID = "validation_f2_impulse/v1"
	ValidationF2SustainedV1         RuleID = "validation_f2_sustained/v1"
	ValidationF3AttributedV1        RuleID = "validation_f3_attributed/v1"
)

// Load reads one field-law artifact under explicit host-selected bounds.
// On error it returns the zero Law, never a partial value. On success it owns
// its strings and slices independently of data and of all other calls.
func Load(data []byte, limits LoadLimits) (Law, error) {
	root, err := readArtifact(data, limits)
	if err != nil {
		return Law{}, err
	}
	if err := checkHead(root, false); err != nil {
		return Law{}, err
	}
	d := decoder{}
	result := d.law(root)
	if d.err != nil {
		return Law{}, d.err
	}
	result.SHA256 = sha256.Sum256(data)
	return result, nil
}
