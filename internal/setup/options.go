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

// Package setup loads the now configuration from $HOME/.now.cnf,
// migrating the previous $HOME/.now file when found.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/canonical/now/internal/cnfembed"
)

// Options holds the settings used to reach the model API.
type Options struct {
	APIURL   string
	APIKey   string
	APIModel string
	// APIType selects the API kind; only "completions-v1" (the completions API)
	// is supported for now.
	APIType string
	// Passthrough holds the merged provider and model passthrough
	// maps, sent verbatim in the request body.
	Passthrough map[string]json.RawMessage
}

// config is the on-disk form of the configuration, decoded from
// the json that cnfembed.ToJSON produces.
type config struct {
	Providers map[string]providerConfig `json:"providers"`
	Models    map[string]modelConfig    `json:"models"`
}

// providerConfig is one entry under providers: the endpoint the
// requests go to, plus passthrough keys for every model on it.
type providerConfig struct {
	APIURL      string                     `json:"api-url"`
	APIKey      string                     `json:"api-key"`
	APIType     string                     `json:"api-type"`
	Passthrough map[string]json.RawMessage `json:"passthrough"`
}

// modelConfig is one entry under models: which provider serves it,
// the name sent to that provider when it differs, and passthrough
// keys specific to this model.
type modelConfig struct {
	Provider      string                     `json:"provider"`
	ProviderModel string                     `json:"provider-model"`
	Passthrough   map[string]json.RawMessage `json:"passthrough"`
}

// Load reads the configuration from $HOME/.now.cnf and resolves
// the given model name. An empty name selects models.default.
// A leftover $HOME/.now is converted to .now.cnf and then removed.
func Load(model string) (*Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot discover home directory: %w", err)
	}
	return loadHome(home, model)
}

// loadHome picks the configuration under the given home directory:
// .now.cnf when present, else a migrated .now, else a not-exist error
// named after .now.cnf so the sample lands on the new path.
func loadHome(home, model string) (*Options, error) {
	newPath := filepath.Join(home, ".now.cnf")
	oldPath := filepath.Join(home, ".now")

	if _, err := os.Stat(newPath); err == nil {
		return LoadFrom(newPath, model)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cannot open %s: %w", newPath, err)
	}

	if _, err := os.Stat(oldPath); err == nil {
		return migrate(oldPath, newPath, model)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cannot open %s: %w", newPath, err)
	}

	return nil, fmt.Errorf("cannot open %s: %w", newPath, fs.ErrNotExist)
}

// DefaultPath is the configuration file path, $HOME/.now.cnf.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot discover home directory: %w", err)
	}
	return filepath.Join(home, ".now.cnf"), nil
}

// LoadFrom reads the configuration in cnf format from the given
// file path and resolves the given model name. An empty name selects
// models.default.
func LoadFrom(path, model string) (*Options, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}
	return parseConfig(path, model, data)
}

// parseConfig translates cnf text to json, decodes it, and checks
// the result. The run uses the named models entry - models.default
// when the name is empty - resolved through its provider into the
// flat Options. Unknown keys anywhere are errors.
func parseConfig(path, model string, data []byte) (*Options, error) {
	jsonData, err := cnfembed.ToJSON(data, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	var cfg config
	dec := json.NewDecoder(bytes.NewReader(jsonData))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	if model == "" {
		model = "default"
	}
	mdl, ok := cfg.Models[model]
	if !ok {
		return nil, fmt.Errorf("cannot load %s: models.%s not found", path, model)
	}
	if mdl.Provider == "" {
		return nil, fmt.Errorf("cannot load %s: models.%s has no provider", path, model)
	}
	prov, ok := cfg.Providers[mdl.Provider]
	if !ok {
		return nil, fmt.Errorf("cannot load %s: models.%s names unknown provider %q", path, model, mdl.Provider)
	}
	if prov.APIURL == "" {
		return nil, fmt.Errorf("cannot load %s: providers.%q has no api-url", path, mdl.Provider)
	}
	if prov.APIType == "" {
		prov.APIType = "completions-v1"
	}
	if prov.APIType != "completions-v1" {
		return nil, fmt.Errorf("cannot load %s: unsupported api-type %q", path, prov.APIType)
	}
	if mdl.ProviderModel == "" {
		mdl.ProviderModel = model
	}
	passthrough, err := mergePassthrough(model, prov.Passthrough, mdl.Passthrough)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Options{
		APIURL:      prov.APIURL,
		APIKey:      prov.APIKey,
		APIType:     prov.APIType,
		APIModel:    mdl.ProviderModel,
		Passthrough: passthrough,
	}, nil
}
