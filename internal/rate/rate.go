package rate

import "strings"

// Estimator defines the interface for rate estimation engines.
type Estimator interface {
    Estimate(fromCountry, toCountry, carrierCode string, weightOz float64) (currency string, amount float64, carrier string)
}

// Dummy implements a simple heuristic equivalent to the current /rates logic.
type Dummy struct{}

func NewDummy() *Dummy { return &Dummy{} }

func (d *Dummy) Estimate(fromCountry, toCountry, carrierCode string, weightOz float64) (string, float64, string) {
    amount := 5.0 + weightOz*0.5
    if !strings.EqualFold(fromCountry, toCountry) {
        amount += 3.0
    }
    if strings.EqualFold(carrierCode, "dhl") {
        amount += 2.0
    }
    return "USD", amount, carrierCode
}

// Karrio is a placeholder estimator for the karrio provider.
// For now it mirrors Dummy's heuristic to keep behavior consistent.
type Karrio struct{}

func NewKarrio() *Karrio { return &Karrio{} }

func (k *Karrio) Estimate(fromCountry, toCountry, carrierCode string, weightOz float64) (string, float64, string) {
    amount := 5.0 + weightOz*0.5
    if !strings.EqualFold(fromCountry, toCountry) {
        amount += 3.0
    }
    if strings.EqualFold(carrierCode, "dhl") {
        amount += 2.0
    }
    return "USD", amount, carrierCode
}

// NewByName returns an Estimator by provider name.
// Currently only "dummy" is supported; unknown names fallback to Dummy.
func NewByName(name string) Estimator {
    switch strings.ToLower(strings.TrimSpace(name)) {
    case "dummy", "":
        return NewDummy()
    case "karrio":
        return NewKarrio()
    default:
        // Placeholder: future provider (e.g., karrio)
        return NewDummy()
    }
}