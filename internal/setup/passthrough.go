// Copyright 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package setup

import (
	"encoding/json"
	"fmt"
)

// mergePassthrough merges the provider-level and model-level
// passthrough maps, model over provider. The merge is deep: a
// key held as a map on both sides merges recursively, and any
// other value from the model side replaces the provider's -
// over always wins the shape.
func mergePassthrough(modelName string, prov, mdl map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if prov == nil && mdl == nil {
		return nil, nil
	}
	merged := make(map[string]json.RawMessage, len(prov)+len(mdl))
	for key, base := range prov {
		merged[key] = base
	}
	for key, over := range mdl {
		base, found := merged[key]
		if !found {
			merged[key] = over
			continue
		}
		combined, err := deepMerge(key, base, over)
		if err != nil {
			return nil, fmt.Errorf("cannot load models.%s: passthrough %q: %v", modelName, key, err)
		}
		merged[key] = combined
	}
	return merged, nil
}

// deepMerge combines two json values base (provider) and over
// (model) under path: maps on both sides merge recursively, and
// any other over value replaces base wholesale - over always
// wins the shape.
func deepMerge(mkey string, base, over json.RawMessage) (json.RawMessage, error) {
	var baseMap, overMap map[string]json.RawMessage
	baseIsMap := json.Unmarshal(base, &baseMap) == nil
	overIsMap := json.Unmarshal(over, &overMap) == nil
	if !baseIsMap || !overIsMap {
		return over, nil
	}
	for key, val := range overMap {
		if sub, found := baseMap[key]; found {
			var err error
			val, err = deepMerge(key, sub, val)
			if err != nil {
				return nil, fmt.Errorf("%q: %v", mkey, err)
			}
		}
		baseMap[key] = val
	}
	return json.Marshal(baseMap)
}
