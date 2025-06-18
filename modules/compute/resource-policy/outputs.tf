/**
  * Copyright 2024 Google LLC
  *
  * Licensed under the Apache License, Version 2.0 (the "License");
  * you may not use this file except in compliance with the License.
  * You may obtain a copy of the License at
  *
  *      http://www.apache.org/licenses/LICENSE-2.0
  *
  * Unless required by applicable law or agreed to in writing, software
  * distributed under the License is distributed on an "AS IS" BASIS,
  * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  * See the License for the specific language governing permissions and
  * limitations under the License.
  */

output "placement_policy" {
  description = <<-EOT
  List of Group placement policies to use for placing VMs or GKE nodes placement. Each object in the list represents one created policy. `COMPACT` is the only supported value for `type` currently. `name` is the name of the placement policy.
  It is assumed that the specified policy exists. To create a placement policy refer to https://cloud.google.com/sdk/gcloud/reference/compute/resource-policies/create/group-placement.
  Note: Placement policies have the [following](https://cloud.google.com/compute/docs/instances/placement-policies-overview#restrictions-compact-policies) restrictions.
  EOT

  value = [for policy in google_compute_resource_policy.policy : {
    type = (var.group_placement_max_distance > 0 || var.workload_policy.type != null) ? "COMPACT" : null
    name = (var.group_placement_max_distance > 0 || var.workload_policy.type != null) ? policy.name : null
  }]
}
