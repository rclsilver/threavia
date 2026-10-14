package domain

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

const FeatureModelSelection Feature = "MODEL_SELECTION"

type ModelOption struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
}

type ModelCatalog struct {
	Models        []ModelOption `json:"models"`
	DefaultModel  string        `json:"defaultModel,omitempty"`
	DefaultEffort string        `json:"defaultEffort,omitempty"`
}

// ResolveModel checks session preferences against the backend's advertised
// capabilities and resolves known defaults. Unknown defaults stay empty.
func (b BackendInstance) ResolveModel(model, effort string) (string, string, error) {
	for _, v := range []struct {
		value string
		limit int
	}{{model, 200}, {effort, 32}} {
		if len(v.value) > v.limit || strings.TrimSpace(v.value) != v.value || strings.ContainsFunc(v.value, unicode.IsControl) {
			return "", "", fmt.Errorf("invalid model or effort value")
		}
	}
	if model == "" && effort == "" && b.ModelCatalog == nil {
		return "", "", nil
	}
	if (model != "" || effort != "") && !slices.Contains(b.Features, FeatureModelSelection) {
		return "", "", fmt.Errorf("this backend does not support model selection")
	}
	if b.ModelCatalog == nil {
		return "", "", fmt.Errorf("this backend has not reported its models")
	}
	catalog := b.ModelCatalog
	selected := model
	if selected == "" {
		selected = catalog.DefaultModel
	}
	var option *ModelOption
	for i := range catalog.Models {
		if catalog.Models[i].ID == selected {
			option = &catalog.Models[i]
			break
		}
	}
	if model != "" && option == nil {
		return "", "", fmt.Errorf("model %q is unavailable on this backend", selected)
	}
	if effort != "" && (option == nil || !slices.Contains(option.Efforts, effort)) {
		return "", "", fmt.Errorf("effort %q is unavailable for model %q", effort, selected)
	}
	if effort == "" {
		if model == "" {
			effort = catalog.DefaultEffort
		}
		if effort == "" && option != nil {
			effort = option.DefaultEffort
		}
	}
	return selected, effort, nil
}
