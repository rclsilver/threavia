package domain

import "testing"

func TestModelPreferencesRespectProviderCapabilities(t *testing.T) {
	b := BackendInstance{Features: []Feature{FeatureModelSelection}, ModelCatalog: &ModelCatalog{
		DefaultModel: "sonnet", DefaultEffort: "high",
		Models: []ModelOption{{ID: "sonnet", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high"}, {ID: "opus", Efforts: []string{"low", "medium", "high", "max"}, DefaultEffort: "high"}, {ID: "haiku"}},
	}}
	for _, test := range []struct {
		model, effort, wantModel, wantEffort string
		valid                                bool
	}{
		{"", "", "sonnet", "high", true}, {"", "low", "sonnet", "low", true},
		{"opus", "max", "opus", "max", true}, {"opus", "", "opus", "high", true},
		{"haiku", "", "haiku", "", true}, {"sonnet", "max", "", "", false},
		{"haiku", "high", "", "", false}, {"unknown", "", "", "", false},
		{"sonnet\n", "", "", "", false}, {" sonnet", "", "", "", false},
	} {
		t.Run(test.model+"/"+test.effort, func(t *testing.T) {
			model, effort, err := b.ResolveModel(test.model, test.effort)
			if (err == nil) != test.valid || model != test.wantModel || effort != test.wantEffort {
				t.Fatalf("got (%q,%q,%v)", model, effort, err)
			}
		})
	}
	old := BackendInstance{}
	if _, _, err := old.ResolveModel("", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := old.ResolveModel("opus", ""); err == nil {
		t.Fatal("old backend accepted an unsupported preference")
	}
}
