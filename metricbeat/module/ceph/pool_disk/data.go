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

package pool_disk

import (
	"encoding/json"

	"github.com/elastic/elastic-agent-libs/logp"
	"github.com/elastic/elastic-agent-libs/mapstr"
)

// Stats represents the statistics for a pool
type Stats struct {
	BytesUsed int64 `json:"bytes_used"`
	MaxAvail  int64 `json:"max_avail"`
	Objects   int64 `json:"objects"`
	KbUsed    int64 `json:"kb_used"`
}

// Pool represents a given Ceph pool
type Pool struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Stats Stats  `json:"stats"`
}

// Output is a list of pools from the response
type Output struct {
	Pools []Pool `json:"pools"`
}

// DfRequest is the df response object
type DfRequest struct {
	Status string `json:"status"`
	Output Output `json:"output"`
}

func eventsMapping(content []byte, logger *logp.Logger) []mapstr.M {
	var d DfRequest
	err := json.Unmarshal(content, &d)
	if err != nil {
		logger.Errorf("Error: %+v", err)
	}

	events := []mapstr.M{}

	for _, Pool := range d.Output.Pools {
		event := mapstr.M{
			"name": Pool.Name,
			"id":   Pool.ID,
			"stats": mapstr.M{
				"used": mapstr.M{
					"bytes": Pool.Stats.BytesUsed,
					"kb":    Pool.Stats.KbUsed,
				},
				"available": mapstr.M{
					"bytes": Pool.Stats.MaxAvail,
				},
				"objects": Pool.Stats.Objects,
			},
		}

		events = append(events, event)

	}

	return events
}
