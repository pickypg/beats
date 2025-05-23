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

//go:build integration

package jetstream

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elastic/beats/v7/libbeat/tests/compose"
	mbtest "github.com/elastic/beats/v7/metricbeat/mb/testing"
)

func TestData(t *testing.T) {
	compose.EnsureUp(t, "nats")
	service := compose.EnsureUp(t, "nats-routes")

	m := mbtest.NewFetcher(t, getConfig(service.Host()))
	m.WriteEvents(t, "")
}

func TestFetch(t *testing.T) {
	compose.EnsureUp(t, "nats")
	service := compose.EnsureUp(t, "nats-routes")

	reporter := &mbtest.CapturingReporterV2{}

	metricSet := mbtest.NewReportingMetricSetV2Error(t, getConfig(service.Host()))
	err := metricSet.Fetch(reporter)
	assert.NoError(t, err)

	e := mbtest.StandardizeEvent(metricSet, reporter.GetEvents()[0])
	t.Logf("%s/%s event: %+v", metricSet.Module().Name(), metricSet.Name(), e.Fields.StringToPrint())
}

func getConfig(host string) map[string]interface{} {
	return map[string]interface{}{
		"module":     "nats",
		"metricsets": []string{"jetstream"},
		"hosts":      []string{host},
		"jetstream": map[string]interface{}{
			"stats": map[string]interface{}{
				"enabled": true,
			},
			"account": map[string]interface{}{
				"enabled": true,
			},
			"stream": map[string]interface{}{
				"enabled": true,
			},
			"consumer": map[string]interface{}{
				"enabled": true,
			},
		},
	}
}
