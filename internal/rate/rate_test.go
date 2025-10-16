package rate

import "testing"

func TestDummyEstimate_DomesticUPS(t *testing.T) {
    est := NewDummy()
    currency, amount, carrier := est.Estimate("US", "US", "ups", 16)
    if currency != "USD" || carrier != "ups" {
        t.Fatalf("unexpected currency/carrier: %s/%s", currency, carrier)
    }
    // 5 + 16*0.5 = 13; domestic, UPS no surcharge
    if amount < 12.9 || amount > 13.1 {
        t.Fatalf("unexpected amount: %v", amount)
    }
}

func TestDummyEstimate_InternationalDHL(t *testing.T) {
    est := NewDummy()
    currency, amount, carrier := est.Estimate("US", "JP", "dhl", 10)
    if currency != "USD" || carrier != "dhl" {
        t.Fatalf("unexpected currency/carrier: %s/%s", currency, carrier)
    }
    // 5 + 10*0.5 = 10; +3 international +2 DHL = 15
    if amount < 14.9 || amount > 15.1 {
        t.Fatalf("unexpected amount: %v", amount)
    }
}

func TestKarrioEstimatorByName(t *testing.T) {
    est := NewByName("karrio")
    if _, ok := est.(*Karrio); !ok {
        t.Fatalf("expected *Karrio from NewByName('karrio')")
    }
    currency, amount, carrier := est.Estimate("US", "JP", "dhl", 10)
    if currency != "USD" || carrier != "dhl" {
        t.Fatalf("unexpected currency/carrier: %s/%s", currency, carrier)
    }
    if amount < 14.9 || amount > 15.1 { // 5 + 10*0.5 +3 intl +2 DHL
        t.Fatalf("unexpected amount: %v", amount)
    }
}