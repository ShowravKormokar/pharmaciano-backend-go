package security

type RiskInput struct {
	IPChanged      bool
	NewDevice      bool
	GeoChanged     bool
	MultipleFailed bool
}

func CalculateRisk(r RiskInput) int {
	score := 0
	if r.IPChanged {
		score += 30
	}
	if r.NewDevice {
		score += 25
	}
	if r.GeoChanged {
		score += 40
	}
	if r.MultipleFailed {
		score += 20
	}
	return score
}
