package service

import (
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestAMoveKeepsWhatTheDestinationOffersAndFallsBackOnTheRest(t *testing.T) {
	b := domain.BackendInstance{Features: []domain.Feature{domain.FeatureModelSelection}, ModelCatalog: &domain.ModelCatalog{
		DefaultModel: "sonnet", DefaultEffort: "high",
		Models: []domain.ModelOption{{ID: "sonnet", Efforts: []string{"low", "high"}, DefaultEffort: "high"}, {ID: "opus", Efforts: []string{"high", "max"}, DefaultEffort: "high"}},
	}}
	legacy := domain.BackendInstance{}
	for _, test := range []struct {
		name                    string
		backend                 domain.BackendInstance
		model, effort           string
		explicitModel, explicit bool
		wantModel, wantEffort   string
		refused                 bool
	}{
		{name: "offered", backend: b, model: "opus", effort: "max", wantModel: "opus", wantEffort: "max"},
		{name: "default", backend: b, wantModel: "", wantEffort: ""},
		{name: "effort not offered for the model", backend: b, model: "sonnet", effort: "max", wantModel: "sonnet", wantEffort: ""},
		{name: "model not offered", backend: b, model: "gpt-5", effort: "max", wantModel: "", wantEffort: ""},
		{name: "model not offered, effort offered by the default", backend: b, model: "gpt-5", effort: "low", wantModel: "", wantEffort: "low"},
		{name: "no model selection there", backend: legacy, model: "opus", effort: "max", wantModel: "", wantEffort: ""},
		{name: "asked model not offered", backend: b, model: "gpt-5", explicitModel: true, refused: true},
		{name: "asked model, carried effort", backend: b, model: "sonnet", effort: "max", explicitModel: true, wantModel: "sonnet", wantEffort: ""},
		{name: "asked effort not offered", backend: b, model: "sonnet", effort: "max", explicit: true, refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, effort, err := destinationModel(test.backend, test.model, test.effort, test.explicitModel, test.explicit)
			if test.refused {
				if err == nil {
					t.Fatalf("got %q/%q, want a refusal", model, effort)
				}
				return
			}
			if err != nil || model != test.wantModel || effort != test.wantEffort {
				t.Fatalf("got %q/%q (%v), want %q/%q", model, effort, err, test.wantModel, test.wantEffort)
			}
		})
	}
}
