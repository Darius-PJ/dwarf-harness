package law

type sourceRule struct {
	id      RuleID
	kind    Kind
	mode    Mode
	profile Profile
}

// Ordered by rule ID. Returning the table by value keeps no mutable registry
// shared by calls; no error depends on map iteration.
func rulesV1() []sourceRule {
	return []sourceRule{
		{BaseStalenessV1, EpistemicUncertainty, Rising, LocalV1},
		{CanonicalChangeV1, Perturbation, Impulse, DependencyDownstreamV1},
		{CheckDurationAnomalyV1, Surprise, Impulse, LocalV1},
		{CheckFlakinessV1, EpistemicUncertainty, Level, LocalV1},
		{CheckOutcomeAnomalyV1, Surprise, Impulse, LocalV1},
		{CheckQuarantineV1, EpistemicUncertainty, Level, LocalV1},
		{CheckRetirementV1, EpistemicUncertainty, Level, LocalV1},
		{CoreBudgetExposureV1, ResourcePressure, Level, RegisteredResourceOutwardV1},
		{CoreResourceUsageV1, ResourcePressure, Level, RegisteredResourceOutwardV1},
		{CoreRunnerQueueV1, ResourcePressure, Level, RegisteredResourceOutwardV1},
		{FederatedPerturbationV1, Perturbation, Impulse, DependencyDownstreamV1},
		{PrivateOverlapV1, EpistemicUncertainty, Level, LocalV1},
		{PromotionOverlapV1, EpistemicUncertainty, Impulse, OverlapRelevanceV1},
		{ProviderStatusV1, Perturbation, Level, DependencyDownstreamV1},
		{RepairR2ImpulseV1, Affordance, Impulse, DependencyDownstreamV1},
		{RepairR3ImpulseV1, Affordance, Impulse, DependencyDownstreamV1},
		{RepairR3SustainedV1, Affordance, Sustained, DependencyDownstreamV1},
		{RouterHeadroomV1, ResourcePressure, Level, RegisteredResourceOutwardV1},
		{SharedResourcePressureAnomalyV1, Surprise, Impulse, LocalV1},
		{ValidationF2ImpulseV1, DependencyStrain, Impulse, LocalV1},
		{ValidationF2SustainedV1, DependencyStrain, Sustained, LocalV1},
		{ValidationF3AttributedV1, DependencyStrain, Sustained, DependencyDownstreamV1},
	}
}

func kindUnit(kind Kind) Unit {
	switch kind {
	case Affordance:
		return AffordanceUnits
	case DependencyStrain:
		return StrainUnits
	case EpistemicUncertainty:
		return UncertaintyUnits
	case Perturbation:
		return PerturbationUnits
	case ResourcePressure:
		return PressureUnits
	case Surprise:
		return SurpriseUnits
	default:
		return ""
	}
}

func profileCoupling(profile Profile) Coupling {
	switch profile {
	case DependencyDownstreamV1, OverlapRelevanceV1:
		return Coupling{profile, DependencyToConsumer, []EdgeType{DeclaredDataflow, StaticDependency}}
	case RegisteredResourceOutwardV1:
		return Coupling{profile, ResourceToConsumer, []EdgeType{RegisteredResourceUse}}
	default:
		return Coupling{profile, DirectionLocal, []EdgeType{}}
	}
}
