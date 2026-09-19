package wire

import (
	"maps"
	"slices"

	"github.com/coder/acp-go-sdk"
)

// ModelRow is one model the harness enumerated for itself.
type ModelRow struct {
	// ID is the invokable model id and the select value.
	ID string
	// Name is the display name; empty falls back to ID.
	Name string
	// Description is attached when non-empty.
	Description string
	// Meta holds the authoritative model metadata published under
	// _meta.<vendor> beside modelId; nil carries none.
	Meta map[string]any
}

// ModelSelectOptions builds the model menu: native rows first, then each
// configured id the rows lack, then the current model when nothing else
// names it. Duplicates and empty ids are dropped; a native row publishes
// modelId with its metadata, a configured or current entry publishes the id
// alone.
func ModelSelectOptions(vendor, current string, native []ModelRow, configured []string) acp.SessionConfigSelectOptionsUngrouped {
	values := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(native)+len(configured)+1)
	seen := make(map[string]struct{}, len(native)+len(configured)+1)

	for _, row := range native {
		if _, ok := seen[row.ID]; ok || row.ID == "" {
			continue
		}

		seen[row.ID] = struct{}{}

		meta := make(map[string]any, len(row.Meta)+1)
		maps.Copy(meta, row.Meta)
		meta["modelId"] = row.ID

		option := acp.SessionConfigSelectOption{
			Name:  row.Name,
			Value: acp.SessionConfigValueId(row.ID),
			Meta:  map[string]any{vendor: meta},
		}
		if option.Name == "" {
			option.Name = row.ID
		}

		if row.Description != "" {
			description := row.Description
			option.Description = &description
		}

		values = append(values, option)
	}

	for _, id := range append(slices.Clone(configured), current) {
		if _, ok := seen[id]; ok || id == "" {
			continue
		}

		seen[id] = struct{}{}
		values = append(values, acp.SessionConfigSelectOption{Name: id, Value: acp.SessionConfigValueId(id)})
	}

	return values
}
