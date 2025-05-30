// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package autodiscover

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elastic/elastic-agent-autodiscover/bus"
	"github.com/elastic/elastic-agent-libs/config"
	"github.com/elastic/elastic-agent-libs/keystore"
	"github.com/elastic/elastic-agent-libs/logp"
	"github.com/elastic/go-ucfg"
)

// Builder provides an interface by which configs can be built from provider metadata
type Builder interface {
	// CreateConfig creates a config from hints passed from providers
	CreateConfig(event bus.Event, options ...ucfg.Option) []*config.C
}

// builders is a struct of Builder list objects and a `keystoreProvider`, which
// has access to a keystores registry
type Builders struct {
	builders         []Builder
	keystoreProvider bus.KeystoreProvider
}

// BuilderConstructor is a func used to generate a Builder object
type BuilderConstructor func(c *config.C, logger *logp.Logger) (Builder, error)

// AddBuilder registers a new BuilderConstructor
func (r *registry) AddBuilder(name string, builder BuilderConstructor) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if name == "" {
		return fmt.Errorf("builder name is required")
	}

	_, exists := r.builders[name]
	if exists {
		return fmt.Errorf("builder '%s' is already registered", name)
	}

	if builder == nil {
		return fmt.Errorf("builder '%s' cannot be registered with a nil factory", name)
	}

	r.builders[name] = builder
	r.logger.Debugf("Builder registered: %s", name)
	return nil
}

// GetBuilder returns the provider with the giving name, nil if it doesn't exist
func (r *registry) GetBuilder(name string) BuilderConstructor {
	r.lock.RLock()
	defer r.lock.RUnlock()

	name = strings.ToLower(name)
	return r.builders[name]
}

// BuildBuilder reads provider configuration and instantiate one
func (r *registry) BuildBuilder(c *config.C) (Builder, error) {
	var config BuilderConfig
	err := c.Unpack(&config)
	if err != nil {
		return nil, err
	}

	builder := r.GetBuilder(config.Type)
	if builder == nil {
		return nil, fmt.Errorf("unknown autodiscover builder %s", config.Type)
	}

	return builder(c, r.logger)
}

// GetConfig creates configs for all builders initialized.
func (b Builders) GetConfig(event bus.Event) []*config.C {
	configs := []*config.C{}
	var opts []ucfg.Option

	if b.keystoreProvider != nil {
		k8sKeystore := b.keystoreProvider.GetKeystore(event)
		if k8sKeystore != nil {
			opts = []ucfg.Option{
				ucfg.Resolve(keystore.ResolverWrap(k8sKeystore)),
			}
		}
	}
	for _, builder := range b.builders {
		if config := builder.CreateConfig(event, opts...); config != nil {
			configs = append(configs, config...)
		}
	}

	return configs
}

// NewBuilders instances the given list of builders. hintsCfg holds `hints` settings
// for simplified mode (single 'hints' builder), `keystoreProvider` has access to keystore registry
func NewBuilders(
	bConfigs []*config.C,
	hintsCfg *config.C,
	keystoreProvider bus.KeystoreProvider,
) (Builders, error) {
	var builders Builders
	if hintsCfg.Enabled() {
		if len(bConfigs) > 0 {
			return Builders{}, errors.New("hints.enabled is incompatible with manually defining builders")
		}

		// pass rest of hints settings to the builder
		err := hintsCfg.SetString("type", -1, "hints")
		if err != nil {
			return Builders{}, fmt.Errorf("autodiscover NewBuilder: could not set 'type' to 'hints' on hints config: %w", err)
		}
		bConfigs = append(bConfigs, hintsCfg)
	}

	for _, bcfg := range bConfigs {
		builder, err := Registry.BuildBuilder(bcfg)
		if err != nil {
			return Builders{}, err
		}
		builders.builders = append(builders.builders, builder)
	}
	builders.keystoreProvider = keystoreProvider
	return builders, nil
}
