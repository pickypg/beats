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

//go:build mage

package main

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/magefile/mage/mg"

	devtools "github.com/elastic/beats/v7/dev-tools/mage"
	heartbeat "github.com/elastic/beats/v7/heartbeat/scripts/mage"

	//mage:import
	"github.com/elastic/beats/v7/dev-tools/mage/target/common"
	//mage:import
	"github.com/elastic/beats/v7/dev-tools/mage/target/build"
	//mage:import
	"github.com/elastic/beats/v7/dev-tools/mage/target/unittest"
	//mage:import
	_ "github.com/elastic/beats/v7/dev-tools/mage/target/integtest/docker"
	//mage:import
	_ "github.com/elastic/beats/v7/dev-tools/mage/target/test"
)

func init() {
	common.RegisterCheckDeps(Update)
	unittest.RegisterPythonTestDeps(Fields)
}

// Package packages the Beat for distribution.
// Use SNAPSHOT=true to build snapshots.
// Use PLATFORMS to control the target platforms.
// Use VERSION_QUALIFIER to control the version qualifier.
func Package() {
	start := time.Now()
	defer func() { fmt.Println("package ran for", time.Since(start)) }()

	devtools.UseElasticBeatOSSPackaging()
	devtools.PackageKibanaDashboardsFromBuildDir()
	heartbeat.CustomizePackaging()

	mg.Deps(Update)
	mg.Deps(build.CrossBuild)
	mg.SerialDeps(devtools.Package, TestPackages)
}

// Package packages the Beat for IronBank distribution.
//
// Use SNAPSHOT=true to build snapshots.
func Ironbank() error {
	fmt.Println(">> Ironbank: this module is not subscribed to the IronBank releases.")
	return nil
}

// TestPackages tests the generated packages (i.e. file modes, owners, groups).
func TestPackages() error {
	return devtools.TestPackages(devtools.WithMonitorsD())
}

func Fields() error {
	return heartbeat.Fields()
}

func GenerateModuleIncludeListGo() error {
	opts := devtools.DefaultIncludeListOptions()
	opts.ImportDirs = append(opts.ImportDirs, "autodiscover/**/*", "monitors/*", "monitors/**/*", "security")
	return devtools.GenerateIncludeListGo(opts)
}

// Update updates the generated files (aka make update).
func Update() {
	mg.SerialDeps(Fields, FieldDocs, Config, GenerateModuleIncludeListGo)
}

func IntegTest() {
	mg.SerialDeps(GoIntegTest)
}

func GoIntegTest(ctx context.Context) error {
	if runtime.GOOS != "windows" {
		return devtools.GoIntegTestFromHost(ctx, devtools.DefaultGoTestIntegrationFromHostArgs())
	}
	return nil
}

func PythonIntegTest() {
	// intentionally blank, CI runs this for every beat
}

func FieldDocs() error {
	return devtools.Docs.FieldDocs("fields.yml")
}

// Config generates both the short/reference/docker configs.
func Config() error {
	return devtools.Config(devtools.AllConfigTypes, heartbeat.ConfigFileParams(), ".")
}
