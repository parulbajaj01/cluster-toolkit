// Copyright 2022 Google LLC
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

package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	googlepb "github.com/golang/protobuf/ptypes/timestamp"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	"gopkg.in/yaml.v3"
)

type TelemetryEvent struct {
	Timestamp string   `json:"timestamp"`
	Event string `json:"event"`
	File string `json:"file"`
	Message string `json:"message"`
	Modules []string `json:"modules"`
}

var TelemetryEnabled = true

func init() {
	if os.Getenv("CLUSTER_TOOLKIT_TELEMETRY") == "false" {
		TelemetryEnabled = false
	}
}

func LogEvent(eventType, filePath, message string, modules []string) {
	if !TelemetryEnabled {
		return
	}
	event := TelemetryEvent{
		Timestamp: time.Now().Format(time.RFC3339),
		Event:eventType,
		File: filePath,
		Message:message,
		Modules: modules,
	}

	logToFile(event)

	sendMetricToCloudDashboard(eventType, filePath, modules, message)
}

func logToFile(event TelemetryEvent) {
	file, err := os.OpenFile("telemetry.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Could not write telemetry:", err)
		return}
	defer file.Close()

	data, err := json.Marshal(event)
	if err != nil {
		fmt.Println("Error in telemetry:", err)
		return}

	file.Write(data)
	file.Write([]byte("\n"))
}

func GetModules(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		log.Printf("telemetry warning: cannot read blueprint %q: %v", path, err)
		return nil
	}

	type Module struct {
		ID string `yaml:"id"`
	}
	type DeploymentGroup struct {
		Modules []Module `yaml:"modules"`
	}
	type Blueprint struct {
		DeploymentGroups []DeploymentGroup `yaml:"deployment_groups"`
	}

	var bp Blueprint
	if err := yaml.Unmarshal(content, &bp); err != nil {
		log.Printf("telemetry warning: invalid blueprint YAML in %q: %v", path, err)
		return nil
	}

	var ids []string
	for _, group := range bp.DeploymentGroups {
		for _, mod := range group.Modules {
			ids = append(ids, mod.ID)
		}
	}
	
	return ids
}

func sendMetricToCloudDashboard(event, filePath string, modules []string, message string) {
	ctx := context.Background()
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		log.Printf("telemetry warning: failed to create monitoring client: %v", err)
		return
	}
	defer client.Close()

	projectID := "ns-playground-a"
	// now := time.Now()

	fileName := filePath
	if idx := strings.LastIndexByte(filePath, os.PathSeparator); idx != -1 {
		fileName = filePath[idx+1:]
	}

	dataPoint := &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{
			EndTime: &googlepb.Timestamp{
				Seconds: time.Now().Unix(),
			},
		},
		Value: &monitoringpb.TypedValue{
			Value: &monitoringpb.TypedValue_Int64Value{
				Int64Value: 1,
			},
		},
	}

	const cloudMonitoringMetricType = "custom.googleapis.com/cluster_toolkit/event_count"
	var timeSeries []*monitoringpb.TimeSeries
	resource := &monitoredrespb.MonitoredResource{
		Type: "global",
		Labels: map[string]string{
			"project_id": projectID,
		},
	}

	if len(modules) == 0 {
		
		timeSeries = append(timeSeries, &monitoringpb.TimeSeries{
			Metric: &metricpb.Metric{
				Type: cloudMonitoringMetricType,
				Labels: map[string]string{
					"blueprint": fileName,
					"event":     event,
					"status":    message,
				},
			},
			Resource: resource,
			Points: []*monitoringpb.Point{dataPoint},
		})
	} else {
	
		for _, moduleName := range modules {
			timeSeries = append(timeSeries, &monitoringpb.TimeSeries{
				Metric: &metricpb.Metric{
					Type: cloudMonitoringMetricType,
					Labels: map[string]string{
						"blueprint": fileName,
						"event": event,
						"status": message,
						"module": moduleName,
					},
				},
				Resource: resource,
				Points:   []*monitoringpb.Point{dataPoint},
			})
		}
	}

	req := &monitoringpb.CreateTimeSeriesRequest{
		Name:       fmt.Sprintf("projects/%s", projectID),
		TimeSeries: timeSeries,
	}

	if err := client.CreateTimeSeries(ctx, req); err != nil {
		log.Printf("telemetry warning: Failed to write time series data: %v", err)
		return
	}

	log.Printf("Telemetry data sent successfully.\n")
}
