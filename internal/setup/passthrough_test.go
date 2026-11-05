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

package setup_test

import (
	"encoding/json"
	"testing"

	"github.com/canonical/now/internal/setup"
)

// passthroughConfig builds a local configuration whose provider and
// default model carry the given passthrough cnf lines.
func passthroughConfig(provider, model string) string {
	content := "providers.local:\n" +
		"    api-url: \"http://127.0.0.1:11434\"\n"
	if provider != "" {
		content += "    passthrough:\n" + provider
	}
	content += "\nmodels.default:\n" +
		"    provider: \"local\"\n"
	if model != "" {
		content += "    passthrough:\n" + model
	}
	return content
}

// assertPassthrough checks that the resolved passthrough holds
// exactly the given key/value pairs.
func assertPassthrough(t *testing.T, got map[string]json.RawMessage, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("passthrough has %d keys, want %d: %#v", len(got), len(want), got)
	}
	for key, val := range want {
		raw, ok := got[key]
		if !ok {
			t.Fatalf("passthrough missing key %q: %#v", key, got)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("passthrough %q does not decode: %v", key, err)
		}
		assertEqual(t, "passthrough["+key+"]", decoded, val)
	}
}

func TestPassthroughNone(t *testing.T) {
	withHome(t, passthroughConfig("", ""))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Passthrough != nil {
		t.Fatalf("passthrough = %#v, want nil", cfg.Passthrough)
	}
}

func TestPassthroughProviderOnly(t *testing.T) {
	withHome(t, passthroughConfig(
		"        temperature: 0.2\n", ""))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPassthrough(t, cfg.Passthrough, map[string]any{"temperature": 0.2})
}

func TestPassthroughModelOverridesProvider(t *testing.T) {
	withHome(t, passthroughConfig(
		"        temperature: 0.9\n"+
			"        max-tokens: 4096\n",
		"        temperature: 0.2\n"))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPassthrough(t, cfg.Passthrough, map[string]any{
		"temperature": 0.2,
		"max-tokens":  float64(4096),
	})
}

func TestPassthroughDeepMerge(t *testing.T) {
	// The provider defaults and the model pin the order, nested
	// in the same provider object - the OpenRouter shape.
	withHome(t, passthroughConfig(
		"        provider:\n"+
			"            allow-fallbacks: false\n"+
			"            data-collection: \"deny\"\n",
		"        provider:\n"+
			"            order: [\"google-vertex/us-east5\"]\n"))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPassthrough(t, cfg.Passthrough, map[string]any{
		"provider": map[string]any{
			"allow-fallbacks": false,
			"data-collection": "deny",
			"order":           []any{"google-vertex/us-east5"},
		},
	})
}

func TestPassthroughDeepMergeNested(t *testing.T) {
	// Two levels down: both sides hold nested maps, and the merge
	// must combine the inner leaves rather than replace the whole
	// nested object.
	withHome(t, passthroughConfig(
		"        outer:\n"+
			"            inner:\n"+
			"                from-provider: 1\n"+
			"                shared: \"provider\"\n",
		"        outer:\n"+
			"            inner:\n"+
			"                from-model: 2\n"+
			"                shared: \"model\"\n"+
			"            sibling: true\n"))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPassthrough(t, cfg.Passthrough, map[string]any{
		"outer": map[string]any{
			"inner": map[string]any{
				"from-provider": float64(1),
				"from-model":    float64(2),
				"shared":        "model",
			},
			"sibling": true,
		},
	})
}

func TestPassthroughScalarReplacesMap(t *testing.T) {
	// Over always wins the shape, in both directions.
	withHome(t, passthroughConfig(
		"        provider:\n"+
			"            order: [\"a\"]\n",
		"        provider: \"pinned\"\n"))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPassthrough(t, cfg.Passthrough, map[string]any{"provider": "pinned"})
}

func TestPassthroughMapOntoScalar(t *testing.T) {
	// The model map replaces the provider scalar wholesale: over
	// always wins the shape, in both directions.
	withHome(t, passthroughConfig(
		"        provider: \"pinned\"\n",
		"        provider:\n"+
			"            order: [\"a\"]\n"))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPassthrough(t, cfg.Passthrough, map[string]any{
		"provider": map[string]any{"order": []any{"a"}},
	})
}

func TestPassthroughModelOverridden(t *testing.T) {
	// The passthrough may set a key the client owns - here model -
	// but the request carries the resolved one: the client's own
	// fields are written after the passthrough.
	withHome(t, passthroughConfig(
		"        model: \"hijacked\"\n", ""))
	cfg, err := setup.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The hijack is visible in the resolved map; the request
	// encoding overlays model on top of it.
	var got string
	if err := json.Unmarshal(cfg.Passthrough["model"], &got); err != nil {
		t.Fatalf("passthrough model does not decode: %v", err)
	}
	assertEqual(t, "passthrough model", got, "hijacked")
	assertEqual(t, "resolved model", cfg.APIModel, "default")
}
