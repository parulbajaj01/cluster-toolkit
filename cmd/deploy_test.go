/*
Copyright 2023 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"hpc-toolkit/pkg/shell"
	"hpc-toolkit/telemetry"
	"os"

	. "gopkg.in/check.v1"
)

func (s *MySuite) TestDeployGroups(c *C) {
	setupMocks()
	defer resetMocks()

	var err error
	pathEnv := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", pathEnv)

	err = deployTerraformGroup(".", getArtifactsDir("."), shell.NeverApply, shell.TextOutput)
	c.Check(err, NotNil)

	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventDeployError)
	c.Check(loggedEvents[0].File, Equals, ".")
	c.Check(loggedEvents[0].Message, Equals, "Terraform configuration failed")

	resetMocks()

	err = deployPackerGroup(".", shell.NeverApply)
	c.Check(err, NotNil)

	c.Check(loggedEvents, HasLen, 1)
	c.Check(loggedEvents[0].Event, Equals, telemetry.EventDeployError)
	c.Check(loggedEvents[0].File, Equals, ".")
	c.Check(loggedEvents[0].Message, Equals, "Terraform configuration failed")
}
