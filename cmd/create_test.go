// Copyright 2023 Google LLC
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

package cmd

import (
	"hpc-toolkit/pkg/config"
	"hpc-toolkit/pkg/modulewriter"
	"hpc-toolkit/telemetry"
	"os"
	"path/filepath"

	"github.com/zclconf/go-cty/cty"
	. "gopkg.in/check.v1"
)

func (s *MySuite) TestSetCLIVariables(c *C) {
	setupMocks()
	defer resetMocks()

	ds := config.DeploymentSettings{
		Vars: config.Dict{}.
			With("deployment_name", cty.StringVal("bush"))}

	vars := []string{
		"project_id=cli_test_project_id",
		"deployment_name=cli_deployment_name",
		"region=cli_region",
		"zone=cli_zone",
		"kv=key=val",
		"keyBool=true",
		"keyInt=15",
		"keyFloat=15.43",
		"keyMap={bar: baz, qux: 1}",
		"keyArray=[1, 2, 3]",
		"keyArrayOfMaps=[foo, {bar: baz, qux: 1}]",
		"keyMapOfArrays={foo: [1, 2, 3], bar: [a, b, c]}",
	}
	c.Assert(setCLIVariables(&ds, vars), IsNil)
	c.Check(
		ds.Vars.Items(), DeepEquals, map[string]cty.Value{
			"project_id":      cty.StringVal("cli_test_project_id"),
			"deployment_name": cty.StringVal("cli_deployment_name"),
			"region":          cty.StringVal("cli_region"),
			"zone":            cty.StringVal("cli_zone"),
			"kv":              cty.StringVal("key=val"),
			"keyBool":         cty.True,
			"keyInt":          cty.NumberIntVal(15),
			"keyFloat":        cty.NumberFloatVal(15.43),
			"keyMap": cty.ObjectVal(map[string]cty.Value{
				"bar": cty.StringVal("baz"),
				"qux": cty.NumberIntVal(1)}),
			"keyArray": cty.TupleVal([]cty.Value{
				cty.NumberIntVal(1), cty.NumberIntVal(2), cty.NumberIntVal(3)}),
			"keyArrayOfMaps": cty.TupleVal([]cty.Value{
				cty.StringVal("foo"),
				cty.ObjectVal(map[string]cty.Value{
					"bar": cty.StringVal("baz"),
					"qux": cty.NumberIntVal(1)})}),
			"keyMapOfArrays": cty.ObjectVal(map[string]cty.Value{
				"foo": cty.TupleVal([]cty.Value{
					cty.NumberIntVal(1), cty.NumberIntVal(2), cty.NumberIntVal(3)}),
				"bar": cty.TupleVal([]cty.Value{
					cty.StringVal("a"), cty.StringVal("b"), cty.StringVal("c")}),
			}),
		})
	c.Check(loggedEvents, HasLen, 0)

	// Failure: Variable without '='
	setupMocks()
	defer resetMocks()
	ds = config.DeploymentSettings{}
	inv := []string{"project_idcli_test_project_id"}
	c.Check(setCLIVariables(&ds, inv), ErrorMatches, "invalid format: .*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].File, Equals, "project_idcli_test_project_id")
	c.Check(loggedEvents[0].Message, Equals, "invalid format")

	// Failure: Unmarshalable value
	setupMocks()
	defer resetMocks()
	ds = config.DeploymentSettings{}
	inv = []string{"pyrite={gold"}
	c.Check(setCLIVariables(&ds, inv), ErrorMatches, ".*unable to convert.*pyrite.*gold.*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].File, Equals, "pyrite")
	c.Check(loggedEvents[0].Message, Equals, "invalid input")
}

func (s *MySuite) TestSetBackendConfig(c *C) {
	setupMocks()
	defer resetMocks()
	// Success
	vars := []string{
		"taste=sweet",
		"type=green",
		"odor=strong",
	}

	ds := config.DeploymentSettings{}
	c.Assert(setBackendConfig(&ds, vars), IsNil)
	c.Check(loggedEvents, HasLen, 0)

	be := ds.TerraformBackendDefaults
	c.Check(be.Type, Equals, "green")
	c.Check(be.Configuration.Items(), DeepEquals, map[string]cty.Value{
		"taste": cty.StringVal("sweet"),
		"odor":  cty.StringVal("strong"),
	})
}

func (s *MySuite) TestMergeDeploymentSettings(c *C) {
	setupMocks()
	defer resetMocks()

	ds1 := config.DeploymentSettings{
		Vars: config.Dict{}.
			With("project_id", cty.StringVal("ds_test_project_id")).
			With("deployment_name", cty.StringVal("ds_deployment_name"))}

	bp1 := config.Blueprint{
		Vars: config.Dict{}.
			With("project_id", cty.StringVal("bp_test_project_id")).
			With("example_var", cty.StringVal("bp_example_value"))}

	// test priority-based merging of deployment variables
	mergeDeploymentSettings(&bp1, ds1)
	c.Check(loggedEvents, HasLen, 0)
	c.Check(bp1.Vars.Items(), DeepEquals, map[string]cty.Value{
		"project_id":      cty.StringVal("ds_test_project_id"),
		"deployment_name": cty.StringVal("ds_deployment_name"),
		"example_var":     cty.StringVal("bp_example_value"),
	})

	// check merging zero-value backends
	setupMocks()
	defer resetMocks()
	ds2 := config.DeploymentSettings{
		TerraformBackendDefaults: config.TerraformBackend{},
	}
	bp2 := config.Blueprint{
		TerraformBackendDefaults: config.TerraformBackend{},
	}
	mergeDeploymentSettings(&bp2, ds2)
	c.Check(loggedEvents, HasLen, 0)
	c.Check(bp2.TerraformBackendDefaults, DeepEquals, config.TerraformBackend{})

	// check keeping blueprint defined backend with no backend in deployment file
	setupMocks()
	defer resetMocks()
	bp3 := config.Blueprint{
		TerraformBackendDefaults: config.TerraformBackend{
			Type: "gsc",
			Configuration: config.NewDict(map[string]cty.Value{
				"bucket": cty.StringVal("bp_bucket"),
			}),
		},
	}
	mergeDeploymentSettings(&bp3, ds2)
	c.Check(loggedEvents, HasLen, 0)
	c.Check(bp3.TerraformBackendDefaults, DeepEquals, config.TerraformBackend{
		Type: "gsc",
		Configuration: config.NewDict(map[string]cty.Value{
			"bucket": cty.StringVal("bp_bucket"),
		}),
	})

	// check overriding blueprint defined backend with deployment file
	setupMocks()
	defer resetMocks()
	ds3 := config.DeploymentSettings{
		TerraformBackendDefaults: config.TerraformBackend{
			Type: "gsc",
			Configuration: config.NewDict(map[string]cty.Value{
				"bucket": cty.StringVal("ds_bucket"),
			}),
		},
	}
	mergeDeploymentSettings(&bp3, ds3)
	c.Check(loggedEvents, HasLen, 0)
	c.Check(bp3.TerraformBackendDefaults, DeepEquals, config.TerraformBackend{
		Type: "gsc",
		Configuration: config.NewDict(map[string]cty.Value{
			"bucket": cty.StringVal("ds_bucket"),
		}),
	})
}

func (s *MySuite) TestSetBackendConfig_Invalid(c *C) {
	setupMocks()
	defer resetMocks()
	// Failure: Variable without '='
	vars := []string{
		"typegreen",
	}
	ds := config.DeploymentSettings{}
	c.Assert(setBackendConfig(&ds, vars), ErrorMatches, "invalid format: .*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].File, Equals, "typegreen")
	c.Check(loggedEvents[0].Message, Equals, "invalid format")
}

func (s *MySuite) TestSetBackendConfig_NoOp(c *C) {
	setupMocks()
	defer resetMocks()
	ds := config.DeploymentSettings{
		TerraformBackendDefaults: config.TerraformBackend{
			Type: "green"}}

	c.Assert(setBackendConfig(&ds, []string{}), IsNil)
	c.Check(loggedEvents, HasLen, 0)
	c.Check(ds.TerraformBackendDefaults, DeepEquals, config.TerraformBackend{
		Type: "green"})
}

func (s *MySuite) TestValidationLevels(c *C) {
	setupMocks()
	defer resetMocks()
	bp := config.Blueprint{}

	c.Check(setValidationLevel(&bp, "ERROR"), IsNil)
	c.Check(bp.ValidationLevel, Equals, config.ValidationError)
	c.Check(loggedEvents, HasLen, 0)

	c.Check(setValidationLevel(&bp, "WARNING"), IsNil)
	c.Check(bp.ValidationLevel, Equals, config.ValidationWarning)
	c.Check(loggedEvents, HasLen, 0)

	c.Check(setValidationLevel(&bp, "IGNORE"), IsNil)
	c.Check(bp.ValidationLevel, Equals, config.ValidationIgnore)
	c.Check(loggedEvents, HasLen, 0)

	c.Check(setValidationLevel(&bp, "INVALID"), NotNil)
	c.Check(loggedEvents, HasLen, 0)
}

func (s *MySuite) TestValidateMaybeDie(c *C) {
	setupMocks()
	defer resetMocks()
	bp := config.Blueprint{
		BlueprintName:   "test_blueprint.yaml",
		Validators:      []config.Validator{{Validator: "invalid"}},
		ValidationLevel: config.ValidationWarning,
	}
	ctx, _ := config.NewYamlCtx([]byte{})
	validateMaybeDie(bp, ctx) // smoke test
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventType("validation_error"))
	c.Check(loggedEvents[0].File, Equals, "test_blueprint.yaml")
	c.Check(loggedEvents[0].Message, Equals, "There was an error in validation")
}

func (s *MySuite) TestIsOverwriteAllowed_Absent(c *C) {
	setupMocks()
	defer resetMocks()
	testDir := c.MkDir()
	depDir := filepath.Join(testDir, "casper")

	bp := config.Blueprint{}
	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, false /*forceOverwrite*/), IsNil)
	c.Check(loggedEvents, HasLen, 0)
	c.Check(checkOverwriteAllowed(depDir, bp, true /*overwriteFlag*/, false /*forceOverwrite*/), IsNil)
	c.Check(loggedEvents, HasLen, 0)
}

func (s *MySuite) TestIsOverwriteAllowed_NotGHPC(c *C) {
	setupMocks()
	defer resetMocks()
	depDir := c.MkDir() // empty deployment folder considered malformed
	bp := config.Blueprint{BlueprintName: "test_blueprint.yaml"}

	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, false /*forceOverwrite*/),
		ErrorMatches, ".* not a valid GHPC deployment folder.*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].Message, Equals, "Folder does not exist")
	c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

	setupMocks()
	defer resetMocks()
	c.Check(checkOverwriteAllowed(depDir, bp, true /*overwriteFlag*/, false /*forceOverwrite*/),
		ErrorMatches, ".* not a valid GHPC deployment folder.*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].Message, Equals, "Folder does not exist")

	setupMocks()
	defer resetMocks()
	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, true /*forceOverwrite*/), IsNil)
	c.Check(loggedEvents, HasLen, 0)
}

func (s *MySuite) TestIsOverwriteAllowed_NoExpanded(c *C) {
	setupMocks()
	defer resetMocks()
	depDir := c.MkDir() // empty deployment folder considered malformed
	if err := os.MkdirAll(modulewriter.HiddenGhpcDir(depDir), 0755); err != nil {
		c.Fatal(err)
	}

	bp := config.Blueprint{BlueprintName: "test_blueprint.yaml"}
	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, false /*forceOverwrite*/),
		ErrorMatches, ".* changing GHPC version.*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].Message, Equals, "Expanded blueprint file missing")
	c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

	setupMocks()
	defer resetMocks()
	c.Check(checkOverwriteAllowed(depDir, bp, true /*overwriteFlag*/, false /*forceOverwrite*/),
		ErrorMatches, ".* changing GHPC version.*")
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].Message, Equals, "Expanded blueprint file missing")

	setupMocks()
	defer resetMocks()
	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, true /*forceOverwrite*/), IsNil)
	c.Check(loggedEvents, HasLen, 0)
}

func (s *MySuite) TestIsOverwriteAllowed_Malformed(c *C) {
	setupMocks()
	defer resetMocks()
	depDir := c.MkDir() // empty deployment folder considered malformed
	if err := os.MkdirAll(modulewriter.ArtifactsDir(depDir), 0755); err != nil {
		c.Fatal(err)
	}
	expPath := filepath.Join(modulewriter.ArtifactsDir(depDir), "expanded_blueprint.yaml")
	if err := os.WriteFile(expPath, []byte("humus"), 0644); err != nil {
		c.Fatal(err)
	}

	bp := config.Blueprint{BlueprintName: "test_blueprint.yaml"}

	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, false /*forceOverwrite*/), NotNil)
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].Message, Equals, "Terraform configuration failed")
	c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

	setupMocks()
	defer resetMocks()
	c.Check(checkOverwriteAllowed(depDir, bp, true /*overwriteFlag*/, false /*forceOverwrite*/), NotNil)
	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
	c.Check(loggedEvents[0].Message, Equals, "Terraform configuration failed")

	setupMocks()
	defer resetMocks()
	// force
	c.Check(checkOverwriteAllowed(depDir, bp, false /*overwriteFlag*/, true /*forceOverwrite*/), IsNil)
	c.Check(loggedEvents, HasLen, 0)
	setupMocks()
	defer resetMocks()
	c.Check(checkOverwriteAllowed(depDir, bp, true /*overwriteFlag*/, true /*forceOverwrite*/), IsNil)
	c.Check(loggedEvents, HasLen, 0)
}

func (s *MySuite) TestIsOverwriteAllowed_Present(c *C) {
	p := c.MkDir()
	artDir := modulewriter.ArtifactsDir(p)
	if err := os.MkdirAll(artDir, 0755); err != nil {
		c.Fatal(err)
	}

	prev := config.Blueprint{
		GhpcVersion: "TaleOfBygoneYears",
		Groups: []config.Group{
			{Name: "isildur"}}}
	if err := prev.Export(filepath.Join(artDir, "expanded_blueprint.yaml")); err != nil {
		c.Fatal(err)
	}
	noW, yesW, noForce, yesForce := false, true, false, true

	{ // Superset
		setupMocks()
		defer resetMocks()
		bp := config.Blueprint{
			BlueprintName: "test_bp_superset.yaml",
			GhpcVersion:   "TaleOfBygoneYears",
			Groups: []config.Group{
				{Name: "isildur"},
				{Name: "elendil"}}}
		c.Check(checkOverwriteAllowed(p, bp, noW, noForce), ErrorMatches, ".* already exists.*")
		c.Check(loggedEvents, HasLen, 1)
		c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
		c.Check(loggedEvents[0].Message, Equals, "Deployment folder already exists")
		c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

		setupMocks()
		defer resetMocks()
		c.Check(checkOverwriteAllowed(p, bp, yesW, noForce), IsNil)
		c.Check(loggedEvents, HasLen, 0)
	}

	{ // Version mismatch
		setupMocks()
		defer resetMocks()
		bp := config.Blueprint{
			BlueprintName: "test_bp_version_mismatch.yaml",
			GhpcVersion:   "TheAlloyOfLaw",
			Groups: []config.Group{
				{Name: "isildur"}}}
		c.Check(checkOverwriteAllowed(p, bp, noW, noForce), ErrorMatches, ".*ghpc_version has changed.*")
		c.Check(loggedEvents, HasLen, 1)
		c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
		c.Check(loggedEvents[0].Message, Equals, "GhpcVersion has changed")
		c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

		setupMocks()
		defer resetMocks()
		c.Check(checkOverwriteAllowed(p, bp, yesW, noForce), ErrorMatches, ".*ghpc_version has changed.*")
		c.Check(loggedEvents, HasLen, 1)
		c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
		c.Check(loggedEvents[0].Message, Equals, "GhpcVersion has changed")

		setupMocks()
		defer resetMocks()
		c.Check(checkOverwriteAllowed(p, bp, noW, yesForce), IsNil)
		c.Check(loggedEvents, HasLen, 0)
	}

	{ // Subset
		setupMocks()
		defer resetMocks()
		bp := config.Blueprint{
			BlueprintName: "test_bp_subset.yaml",
			GhpcVersion:   "TaleOfBygoneYears",
			Groups: []config.Group{
				{Name: "aragorn"}}}
		c.Check(checkOverwriteAllowed(p, bp, noW, noForce), ErrorMatches, `.* already exists.*`)
		c.Check(loggedEvents, HasLen, 1)
		c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
		c.Check(loggedEvents[0].Message, Equals, "Deployment folder already exists")
		c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

		setupMocks()
		defer resetMocks()
		c.Check(checkOverwriteAllowed(p, bp, yesW, noForce), ErrorMatches, `.*remove a deployment group "isildur".*`)
		c.Check(loggedEvents, HasLen, 1)
		c.Check(loggedEvents[0].Event, Equals, telemetry.EventCreateError)
		c.Check(loggedEvents[0].Message, Equals, "Group is not supported")
		c.Check(loggedEvents[0].File, Equals, bp.BlueprintName)

		setupMocks()
		defer resetMocks()
		c.Check(checkOverwriteAllowed(p, bp, noW, yesForce), IsNil)
		c.Check(loggedEvents, HasLen, 0)
	}
}
