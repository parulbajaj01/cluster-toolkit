// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package cmd defines command line utilities for gcluster
package cmd

import (
	"fmt"
	"hpc-toolkit/pkg/config"
	"hpc-toolkit/pkg/logging"
	"hpc-toolkit/pkg/modulewriter"
	"hpc-toolkit/pkg/shell"
	"path/filepath"

	"hpc-toolkit/telemetry"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func addDeployFlags(c *cobra.Command) *cobra.Command {
	return addJsonOutputFlag(
		addGroupSelectionFlags(
			addAutoApproveFlag(
				addArtifactsDirFlag(
					addCreateFlags(c)))))
}

func init() {
	rootCmd.AddCommand(deployCmd)
}

var (
	deployCmd = addDeployFlags(&cobra.Command{
		Use:               "deploy (<DEPLOYMENT_DIRECTORY> | <BLUEPRINT_FILE>)",
		Short:             "deploy all resources in a Toolkit deployment directory.",
		Long:              "deploy all resources in a Toolkit deployment directory.",
		Args:              cobra.MatchAll(cobra.ExactArgs(1), checkExists),
		ValidArgsFunction: filterYaml,
		Run:               runDeployCmd,
		SilenceUsage:      true,
	})
)

func runDeployCmd(cmd *cobra.Command, args []string) {
	var deplRoot string

	if checkDir(cmd, args) != nil { // arg[0] is BLUEPRINT_FILE
		deplRoot = doCreate(args[0])

		if deplRoot == "" {
			telemetry.LogEvent(telemetry.EventDeployError, args[0], "empty deployment path", nil)
			checkErr(fmt.Errorf("blueprint creation returned empty path"), nil)
			return
		}
	} else { // arg[0] is DEPLOYMENT_DIRECTORY
		deplRoot = args[0]

		// check that no "create" flags were specified
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Changed && createCmd.Flag(f.Name) != nil {
				checkErr(fmt.Errorf("cannot specify flag %q with DEPLOYMENT_DIRECTORY provided", f.Name), nil)
			}
		})
	}

	telemetry.LogEvent(telemetry.EventDeployStart, deplRoot, "Starting deployment", nil)

	defer func() {
		if r := recover(); r != nil {
			telemetry.LogEvent(telemetry.EventDeployError, deplRoot, "Deployment panic", nil)
			panic(r)
		}
	}()

	err := doDeployWithTelemetry(deplRoot)
	if err != nil {
		telemetry.LogEvent(telemetry.EventDeployError, deplRoot, err.Error(), nil)
		return
	}

	telemetry.LogEvent(telemetry.EventDeploySuccess, deplRoot, "Deployment finished successfully", nil)

}

func doDeployWithTelemetry(deplRoot string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during deployment: %v", r)
		}
	}()
	doDeploy(deplRoot)
	return nil
}

func doDeploy(deplRoot string) {
	artDir := getArtifactsDir(deplRoot)
	checkErr(shell.CheckWritableDir(artDir), nil)
	bp, ctx := artifactBlueprintOrDie(artDir)
	groups := bp.Groups
	checkErr(validateGroupSelectionFlags(bp), ctx)
	checkErr(validateRuntimeDependencies(deplRoot, groups), ctx)
	checkErr(shell.ValidateDeploymentDirectory(groups, deplRoot), ctx)

	for ig, group := range groups {
		if !isGroupSelected(group.Name) {
			logging.Info("skipping group %q", group.Name)
			continue
		}

		groupDir := filepath.Join(deplRoot, string(group.Name))
		checkErr(shell.ImportInputs(groupDir, artDir, bp), ctx)

		switch group.Kind() {
		case config.PackerKind:
			// Packer groups are enforced to have length 1
			subPath, e := modulewriter.DeploymentSource(group.Modules[0])
			checkErr(e, ctx)
			moduleDir := filepath.Join(groupDir, subPath)
			checkErr(deployPackerGroup(moduleDir, getApplyBehavior()), ctx)
		case config.TerraformKind:
			checkErr(deployTerraformGroup(groupDir, artDir, getApplyBehavior(), getOutputFormat()), ctx)
		default:
			checkErr(
				config.BpError{
					Err:  fmt.Errorf("group %q is an unsupported kind %q", groupDir, group.Kind()),
					Path: config.Root.Groups.At(ig).Name}, ctx)
		}
	}
	logging.Info("\n###############################")
	printAdvancedInstructionsMessage(deplRoot)
}

func validateRuntimeDependencies(deplDir string, groups []config.Group) error {
	for ig, group := range groups {
		var err error
		switch group.Kind() {
		case config.PackerKind:
			err = shell.ConfigurePacker()
		case config.TerraformKind:
			groupDir := filepath.Join(deplDir, string(group.Name))
			_, err = shell.ConfigureTerraform(groupDir)
		default:
			err = config.BpError{
				Path: config.Root.Groups.At(ig).Name,
				Err:  fmt.Errorf("group %s is an unsupported kind %q", group.Name, group.Kind().String())}
		}
		if err != nil {
			telemetry.LogEvent(telemetry.EventDeployError, deplDir, "Terraform configuration failed", nil)
			return err
		}
	}
	return nil
}

func deployPackerGroup(moduleDir string, applyBehavior shell.ApplyBehavior) error {
	if err := shell.ConfigurePacker(); err != nil {
		telemetry.LogEvent(telemetry.EventDeployError, moduleDir, "Terraform configuration failed", nil)
		return err
	}
	c := shell.ProposedChanges{
		Summary: fmt.Sprintf("Proposed change: use packer to build image in %s", moduleDir),
		Full:    fmt.Sprintf("Proposed change: use packer to build image in %s", moduleDir),
	}
	buildImage := applyBehavior == shell.AutomaticApply || shell.ApplyChangesChoice(c)
	if buildImage {
		logging.Info("initializing packer module at %s", moduleDir)
		if err := shell.ExecPackerCmd(moduleDir, false, "init", "."); err != nil {
			telemetry.LogEvent(telemetry.EventDeployError, moduleDir, "initializing packer module failed", nil)
			return err
		}
		logging.Info("validating packer module at %s", moduleDir)
		if err := shell.ExecPackerCmd(moduleDir, false, "validate", "."); err != nil {
			telemetry.LogEvent(telemetry.EventDeployError, moduleDir, "validating packer module failed", nil)
			return err
		}
		logging.Info("building image using packer module at %s", moduleDir)
		if err := shell.ExecPackerCmd(moduleDir, true, "build", "."); err != nil {
			telemetry.LogEvent(telemetry.EventDeployError, moduleDir, "building image using packer module failed", nil)
			return err
		}
	}
	return nil
}

func deployTerraformGroup(groupDir string, artifactsDir string, applyBehavior shell.ApplyBehavior, outputFormat shell.OutputFormat) error {
	tf, err := shell.ConfigureTerraform(groupDir)
	if err != nil {
		telemetry.LogEvent(telemetry.EventDeployError, groupDir, "Terraform configuration failed", nil)
		return err
	}
	return shell.ExportOutputs(tf, artifactsDir, applyBehavior, outputFormat)
}
