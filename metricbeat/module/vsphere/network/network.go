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

package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	vSphereClientUtil "github.com/elastic/beats/v7/metricbeat/module/vsphere/client"

	"github.com/elastic/beats/v7/metricbeat/module/vsphere/security"

	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/metricbeat/mb"
	"github.com/elastic/beats/v7/metricbeat/module/vsphere"
)

// init registers the MetricSet with the central registry as soon as the program
// starts. The New function will be called later to instantiate an instance of
// the MetricSet for each network is defined in the module's configuration. After the
// MetricSet has been created then Fetch will begin to be called periodically.
func init() {
	mb.Registry.MustAddMetricSet("vsphere", "network", New,
		mb.WithHostParser(vsphere.HostParser),
		mb.DefaultMetricSet(),
	)
}

// MetricSet type defines all fields of the MetricSet.
type NetworkMetricSet struct {
	*vsphere.MetricSet
}

// New creates a new instance of the MetricSet.
func New(base mb.BaseMetricSet) (mb.MetricSet, error) {
	ms, err := vsphere.NewMetricSet(base)
	if err != nil {
		return nil, fmt.Errorf("failed to create vSphere metricset: %w", err)
	}

	security.WarnIfInsecure(ms.Logger(), "network", ms.Insecure)
	return &NetworkMetricSet{ms}, nil
}

type triggeredAlarm struct {
	Name          string      `json:"name"`
	ID            string      `json:"id"`
	Status        string      `json:"status"`
	TriggeredTime common.Time `json:"triggered_time"`
	Description   string      `json:"description"`
	EntityName    string      `json:"entity_name"`
}

type metricData struct {
	assetNames      assetNames
	triggeredAlarms []triggeredAlarm
}

type assetNames struct {
	outputVmNames   []string
	outputHostNames []string
}

// Fetch implements the data gathering and data conversion to the right
// format. It publishes the event which is then forwarded to the output. In case
// of an error set the Error field of mb.Event or simply call report.Error().
func (m *NetworkMetricSet) Fetch(ctx context.Context, reporter mb.ReporterV2) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, err := govmomi.NewClient(ctx, m.HostURL, m.Insecure)
	if err != nil {
		return fmt.Errorf("error in NewClient: %w", err)
	}

	defer func() {
		err := vSphereClientUtil.Logout(ctx, client)

		if err != nil {
			m.Logger().Errorf("error trying to logout from vSphere: %v", err)
		}
	}()

	c := client.Client

	v, err := view.NewManager(c).CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"Network"}, true)
	if err != nil {
		return fmt.Errorf("error in CreateContainerView: %w", err)
	}

	defer func() {
		if err := v.Destroy(ctx); err != nil {
			m.Logger().Errorf("error trying to destroy view from vSphere: %v", err)
		}
	}()

	// Retrieve property for all networks
	var networks []mo.Network
	err = v.Retrieve(ctx, []string{"Network"}, []string{"summary", "name", "overallStatus", "configStatus", "vm", "host", "triggeredAlarmState"}, &networks)
	if err != nil {
		return fmt.Errorf("error in Retrieve: %w", err)
	}

	pc := property.DefaultCollector(c)
	for i := range networks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			assetNames, err := getAssetNames(ctx, pc, &networks[i])
			if err != nil {
				m.Logger().Errorf("Failed to retrieve object from network %s: %v", networks[i].Name, err)
				continue
			}

			triggeredAlarm, err := getTriggeredAlarm(ctx, pc, networks[i].TriggeredAlarmState)
			if err != nil {
				m.Logger().Errorf("Failed to retrieve alerts from network %s: %w", networks[i].Name, err)
			}

			reporter.Event(mb.Event{
				MetricSetFields: m.mapEvent(networks[i], &metricData{assetNames: assetNames, triggeredAlarms: triggeredAlarm}),
			})
		}
	}

	return nil
}

func getAssetNames(ctx context.Context, pc *property.Collector, net *mo.Network) (assetNames, error) {
	referenceList := append(net.Host, net.Vm...)

	outputHostNames := make([]string, 0, len(net.Host))
	outputVmNames := make([]string, 0, len(net.Vm))

	if len(referenceList) == 0 {
		return assetNames{}, nil
	}

	var objects []mo.ManagedEntity
	if err := pc.Retrieve(ctx, referenceList, []string{"name"}, &objects); err != nil {
		return assetNames{}, err
	}

	for _, ob := range objects {
		name := strings.ReplaceAll(ob.Name, ".", "_")
		switch ob.Reference().Type {
		case "HostSystem":
			outputHostNames = append(outputHostNames, name)
		case "VirtualMachine":
			outputVmNames = append(outputVmNames, name)
		}
	}

	return assetNames{
		outputVmNames:   outputVmNames,
		outputHostNames: outputHostNames,
	}, nil
}

func getTriggeredAlarm(ctx context.Context, pc *property.Collector, triggeredAlarmState []types.AlarmState) ([]triggeredAlarm, error) {
	var triggeredAlarms []triggeredAlarm
	for _, alarmState := range triggeredAlarmState {
		var triggeredAlarm triggeredAlarm
		var alarm mo.Alarm
		err := pc.RetrieveOne(ctx, alarmState.Alarm, nil, &alarm)
		if err != nil {
			return nil, err
		}
		triggeredAlarm.Name = alarm.Info.Name

		var entityName string
		if alarmState.Entity.Type == "Network" {
			var entity mo.Network
			if err := pc.RetrieveOne(ctx, alarmState.Entity, []string{"name"}, &entity); err != nil {
				return nil, err
			}

			entityName = entity.Name
		} else {
			var entity mo.ManagedEntity
			if err := pc.RetrieveOne(ctx, alarmState.Entity, []string{"name"}, &entity); err != nil {
				return nil, err
			}

			entityName = entity.Name
		}
		triggeredAlarm.EntityName = entityName

		triggeredAlarm.Description = alarm.Info.Description
		triggeredAlarm.ID = alarmState.Key
		triggeredAlarm.Status = string(alarmState.OverallStatus)
		triggeredAlarm.TriggeredTime = common.Time(alarmState.Time)

		triggeredAlarms = append(triggeredAlarms, triggeredAlarm)
	}

	return triggeredAlarms, nil
}
