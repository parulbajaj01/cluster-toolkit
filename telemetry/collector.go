// Copyright 2025 Google LLC
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

	"hpc-toolkit/pkg/logging"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"cloud.google.com/go/storage"
	googlepb "github.com/golang/protobuf/ptypes/timestamp"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	"gopkg.in/yaml.v3"
)

type EventType string

const (
	EventCreateStart    EventType = "create_start"
	EventCreateSuccess  EventType = "create_success"
	EventCreateError    EventType = "create_error"
	EventDeployStart    EventType = "deploy_start"
	EventDeploySuccess  EventType = "deploy_success"
	EventDeployError    EventType = "deploy_error"
	EventDestroyStart   EventType = "destroy_start"
	EventDestroySuccess EventType = "destroy_success"
	EventDestroyError   EventType = "destroy_error"
)

type TelemetryEvent struct {
	Timestamp string    `json:"timestamp"`
	Event     EventType `json:"event"`
	File      string    `json:"file"`
	Message   string    `json:"message"`
	Modules   []string  `json:"modules"`
}

var TelemetryEnabled = true
var userTelemetryBucket string
var userProjectID string

type GCSClient interface {
	Bucket(name string) GCSBucketHandle
	Close() error
}

type GCSBucketHandle interface {
	Object(name string) GCSObjectHandle
}

type GCSObjectHandle interface {
	NewWriter(ctx context.Context) GCSWriter
}

type GCSWriter interface {
	Write(p []byte) (n int, err error)
	Close() error
	SetContentType(contentType string)
}

type GCSClientWrapper struct {
	client *storage.Client
}

func (w *GCSClientWrapper) Bucket(name string) GCSBucketHandle {
	return &GCSBucketHandleWrapper{handle: w.client.Bucket(name)}
}

func (w *GCSClientWrapper) Close() error {
	return w.client.Close()
}

type GCSBucketHandleWrapper struct {
	handle *storage.BucketHandle
}

func (w *GCSBucketHandleWrapper) Object(name string) GCSObjectHandle {
	return &GCSObjectHandleWrapper{handle: w.handle.Object(name)}
}

type GCSObjectHandleWrapper struct {
	handle *storage.ObjectHandle
}

func (w *GCSObjectHandleWrapper) NewWriter(ctx context.Context) GCSWriter {
	writer := w.handle.NewWriter(ctx)
	return &GCSWriterWrapper{writer: writer}
}

type GCSWriterWrapper struct {
	writer *storage.Writer
}

func (w *GCSWriterWrapper) Write(p []byte) (n int, err error) {
	return w.writer.Write(p)
}

func (w *GCSWriterWrapper) Close() error {
	return w.writer.Close()
}

func (w *GCSWriterWrapper) SetContentType(contentType string) {
	w.writer.ContentType = contentType
}

type CloudMonitoringClient interface {
	CreateTimeSeries(ctx context.Context, req *monitoringpb.CreateTimeSeriesRequest) error
	Close() error
}

type CloudMonitoringClientWrapper struct {
	client *monitoring.MetricClient
}

func (w *CloudMonitoringClientWrapper) CreateTimeSeries(ctx context.Context, req *monitoringpb.CreateTimeSeriesRequest) error {
	return w.client.CreateTimeSeries(ctx, req)
}

func (w *CloudMonitoringClientWrapper) Close() error {
	return w.client.Close()
}

var NewGCSClient = func(ctx context.Context) (GCSClient, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCSClientWrapper{client: client}, nil
}

var NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) {
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		return nil, err
	}
	return &CloudMonitoringClientWrapper{client: client}, nil
}

var osReadFileFunc = os.ReadFile

func init() {
	if os.Getenv("CLUSTER_TOOLKIT_TELEMETRY") == "false" {
		TelemetryEnabled = false
	}
	userProjectID = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")
	userTelemetryBucket = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")
}

var LogEvent = func(eventType EventType, filePath, message string, modules []string) {
	if !TelemetryEnabled {
		return
	}
	event := TelemetryEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Event:     eventType,
		File:      filePath,
		Message:   message,
		Modules:   modules,
	}

	logToFile(event)

	if userTelemetryBucket != "" {
		sendToUserGCSBucket(context.Background(), event)
	}

	sendMetricToCloudDashboard(context.Background(), eventType, filePath, modules, message)
}

var GetModules = func(path string) []string {
	content, err := osReadFileFunc(path)
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
	uniqueIDs := make(map[string]bool)

	for _, group := range bp.DeploymentGroups {
		for _, mod := range group.Modules {
			if _, ok := uniqueIDs[mod.ID]; !ok {
				uniqueIDs[mod.ID] = true
				ids = append(ids, mod.ID)
			}
		}
	}

	return ids
}

func logToFile(event TelemetryEvent) {
	file, err := os.OpenFile("telemetry.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logging.Error("could not write telemetry to file")
		return
	}
	defer file.Close()

	data, err := json.Marshal(event)
	if err != nil {
		logging.Error("error marshaling telemetry event for file")
		return
	}

	file.Write(data)
	file.Write([]byte("\n"))
}

func sendToUserGCSBucket(ctx context.Context, event TelemetryEvent) {
	client, err := NewGCSClient(ctx)
	if err != nil {
		log.Printf("telemetry warning: failed to create GCS client for user bucket: %v", err)
		return
	}
	defer client.Close()

	objectName := fmt.Sprintf("telemetry/logs/%s/%s-%s.json", userProjectID, event.Event, time.Now().Format("2006-01-02T15-04-05Z07:00"))

	wc := client.Bucket(userTelemetryBucket).Object(objectName).NewWriter(ctx)
	wc.SetContentType("application/x-ndjson")

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("telemetry warning: error marshaling telemetry event for GCS: %v", err)
		return
	}

	ndjsonData := append(data, '\n')

	if _, err := wc.Write(ndjsonData); err != nil {
		log.Printf("telemetry warning: failed to write telemetry log to user GCS bucket %q: %v", userTelemetryBucket, err)
		return
	}

	if err := wc.Close(); err != nil {
		log.Printf("telemetry warning: failed to close GCS writer for user bucket %q: %v", userTelemetryBucket, err)
		return
	}

	log.Printf("Telemetry data successfully sent to user GCS bucket %q, object %q.", userTelemetryBucket, objectName)
}

func sendMetricToCloudDashboard(ctx context.Context, event EventType, filePath string, modules []string, message string) {
	client, err := NewMetricClient(ctx)
	if err != nil {
		log.Printf("telemetry warning: failed to create monitoring client: %v", err)
		return
	}
	defer client.Close()

	const projectID = "hpc-toolkit-gsc"

	const maxMessageLength = 1024
	if len(message) > maxMessageLength {
		message = message[:maxMessageLength]
	}

	fileName := filePath
	if idx := strings.LastIndexByte(filePath, os.PathSeparator); idx != -1 {
		fileName = filePath[idx+1:]
	}

	dataPoint := &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{
			EndTime: &googlepb.Timestamp{
				Seconds: time.Now().UTC().Unix(),
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

	timeSeries = append(timeSeries, &monitoringpb.TimeSeries{
		Metric: &metricpb.Metric{
			Type: cloudMonitoringMetricType,
			Labels: map[string]string{
				"blueprint": fileName,
				"event":     string(event),
				"status":    message,
			},
		},
		Resource: resource,
		Points:   []*monitoringpb.Point{dataPoint},
	})

	for _, moduleName := range modules {
		timeSeries = append(timeSeries, &monitoringpb.TimeSeries{
			Metric: &metricpb.Metric{
				Type: cloudMonitoringMetricType,
				Labels: map[string]string{
					"module": moduleName,
				},
			},
			Resource: resource,
			Points:   []*monitoringpb.Point{dataPoint},
		})
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
