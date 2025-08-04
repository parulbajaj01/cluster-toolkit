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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockGCSClient struct {
	mock.Mock
}

func (m *MockGCSClient) Bucket(name string) GCSBucketHandle {
	args := m.Called(name)
	return args.Get(0).(GCSBucketHandle)
}

func (m *MockGCSClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

type MockGCSBucketHandle struct {
	mock.Mock
}

func (m *MockGCSBucketHandle) Object(name string) GCSObjectHandle {
	args := m.Called(name)
	return args.Get(0).(GCSObjectHandle)
}

type MockGCSObjectHandle struct {
	mock.Mock
}

func (m *MockGCSObjectHandle) NewWriter(ctx context.Context) GCSWriter {
	args := m.Called(ctx)
	writer := args.Get(0).(GCSWriter)
	return writer
}

type MockGCSWriter struct {
	mock.Mock
	Buffer      bytes.Buffer
	mu          sync.Mutex
	closed      bool
	ContentType string
}

func (m *MockGCSWriter) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errors.New("writer already closed")
	}
	args := m.Called(p)
	if args.Get(0) != nil {
		n = args.Int(0)
	} else {
		n = len(p)
	}
	if args.Get(1) != nil {
		err = args.Error(1)
	}
	if err == nil {
		m.Buffer.Write(p)
	}
	return
}

func (m *MockGCSWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	args := m.Called()
	return args.Error(0)
}

func (m *MockGCSWriter) SetContentType(contentType string) {
	m.Called(contentType)
	m.ContentType = contentType
}

type MockCloudMonitoringClient struct {
	mock.Mock
}

func (m *MockCloudMonitoringClient) CreateTimeSeries(ctx context.Context, req *monitoringpb.CreateTimeSeriesRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

func (m *MockCloudMonitoringClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func captureLog() (*bytes.Buffer, func()) {
	var buf bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&buf)
	return &buf, func() {
		log.SetOutput(oldOutput)
	}
}

func setEnv(t *testing.T, key, value string) {
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("Failed to set environment variable %s: %v", key, err)
	}
}

func unsetEnv(key string) {
	_ = os.Unsetenv(key)
}

func resetTelemetryState() {
	TelemetryEnabled = true
	userTelemetryBucket = ""
	userProjectID = ""

	unsetEnv("CLUSTER_TOOLKIT_TELEMETRY")
	unsetEnv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")
	unsetEnv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")

	NewGCSClient = func(ctx context.Context) (GCSClient, error) {
		return &MockGCSClient{}, nil
	}
	NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) {
		return &MockCloudMonitoringClient{}, nil
	}
	osReadFileFunc = os.ReadFile
}

func TestMain(m *testing.M) {
	defer resetTelemetryState()
	defer restoreReadFile()
	code := m.Run()
	os.Exit(code)
}

func TestTelemetryEnabledDisabled(t *testing.T) {
	defer resetTelemetryState()

	resetTelemetryState()
	assert.True(t, TelemetryEnabled, "Telemetry should be enabled by default")

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY", "false")
	TelemetryEnabled = true
	if os.Getenv("CLUSTER_TOOLKIT_TELEMETRY") == "false" {
		TelemetryEnabled = false
	}
	assert.False(t, TelemetryEnabled, "Telemetry should be disabled when CLUSTER_TOOLKIT_TELEMETRY is 'false'")

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY", "true")
	TelemetryEnabled = true
	if os.Getenv("CLUSTER_TOOLKIT_TELEMETRY") == "false" {
		TelemetryEnabled = false
	} else {
		TelemetryEnabled = true
	}
	assert.True(t, TelemetryEnabled, "Telemetry should be enabled when CLUSTER_TOOLKIT_TELEMETRY is 'true'")
}

func TestInit_EnvironmentVariables(t *testing.T) {
	defer resetTelemetryState()

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_PROJECT", "test-project")
	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_BUCKET", "test-bucket")

	userProjectID = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")
	userTelemetryBucket = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")

	assert.Equal(t, "test-project", userProjectID, "userProjectID should be set from env var")
	assert.Equal(t, "test-bucket", userTelemetryBucket, "userTelemetryBucket should be set from env var")

	unsetEnv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")
	unsetEnv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")
}

func TestLogEvent_TelemetryDisabled(t *testing.T) {
	defer resetTelemetryState()
	defer os.Remove("telemetry.log")

	TelemetryEnabled = false

	mockGCS := new(MockGCSClient)
	mockMetric := new(MockCloudMonitoringClient)
	NewGCSClient = func(ctx context.Context) (GCSClient, error) { return mockGCS, nil }
	NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) { return mockMetric, nil }

	mockGCS.AssertNotCalled(t, "Bucket", mock.Anything)
	mockMetric.AssertNotCalled(t, "CreateTimeSeries", mock.Anything, mock.Anything)

	buf, restoreLog := captureLog()
	defer restoreLog()

	LogEvent(EventCreateStart, "test.yaml", "starting", []string{"module-a"})

	assert.NoFileExists(t, "telemetry.log", "telemetry.log file should not be created when telemetry is disabled")
	assert.Empty(t, buf.String(), "No log output expected when telemetry is disabled")

	mockGCS.AssertExpectations(t)
	mockMetric.AssertExpectations(t)
}

func TestLogToFile(t *testing.T) {
	defer resetTelemetryState()
	defer os.Remove("telemetry.log")

	event := TelemetryEvent{
		Timestamp: "2023-10-26T10:00:00Z",
		Event:     EventDeploySuccess,
		File:      "/path/to/my/blueprint.yaml",
		Message:   "Deployment finished",
		Modules:   []string{"vpc", "compute"},
	}

	logToFile(event)

	_, err := os.Stat("telemetry.log")
	assert.NoError(t, err, "telemetry.log file should exist")

	content, err := os.ReadFile("telemetry.log")
	assert.NoError(t, err, "should be able to read telemetry.log")

	var loggedEvent TelemetryEvent
	err = json.Unmarshal(bytes.TrimSpace(content), &loggedEvent)
	assert.NoError(t, err, "should unmarshal logged JSON")
	assert.Equal(t, event.Event, loggedEvent.Event)
	assert.Equal(t, event.File, loggedEvent.File)
	assert.Equal(t, event.Message, loggedEvent.Message)
	assert.Equal(t, event.Modules, loggedEvent.Modules)
	assert.NotEmpty(t, loggedEvent.Timestamp, "Timestamp should not be empty")
}

func TestGetModules_ValidBlueprint(t *testing.T) {
	defer resetTelemetryState()

	setMockReadFile([]byte(`
deployment_groups:
  - modules:
      - id: module-a
      - id: module-b
  - modules:
      - id: module-c
`), nil)

	modules := GetModules("test_valid_blueprint.yaml")
	expectedModules := []string{"module-a", "module-b", "module-c"}
	assert.ElementsMatch(t, expectedModules, modules)
}

func TestGetModules_InvalidYAML(t *testing.T) {
	defer resetTelemetryState()

	setMockReadFile([]byte(`
deployment_groups:
  - modules:
      - id: module-a
  invalid_key:
    - bad_indentation
`), nil)

	buf, restoreLog := captureLog()
	defer restoreLog()

	modules := GetModules("test_invalid_blueprint.yaml")
	assert.Empty(t, modules, "Modules should be empty for invalid YAML")
	assert.Contains(t, buf.String(), "telemetry warning: invalid blueprint YAML in", "Expected warning about invalid YAML")
}

func TestGetModules_NonExistentFile(t *testing.T) {
	defer resetTelemetryState()
	setMockReadFile(nil, os.ErrNotExist)

	filePath := "non_existent_blueprint.yaml"

	buf, restoreLog := captureLog()
	defer restoreLog()

	modules := GetModules(filePath)
	assert.Empty(t, modules, "Modules should be empty for non-existent file")
	assert.Contains(t, buf.String(), "telemetry warning: cannot read blueprint", "Expected warning about cannot read blueprint")
}

func TestSendToUserGCSBucket_SimulatedSuccess(t *testing.T) {
	defer resetTelemetryState()

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_BUCKET", "mock-bucket")
	userTelemetryBucket = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")
	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_PROJECT", "test-project-id")
	userProjectID = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")

	mockWriter := new(MockGCSWriter)
	mockWriter.On("Write", mock.Anything).Return(0, nil)
	mockWriter.On("Close").Return(nil)
	mockWriter.On("SetContentType", "application/x-ndjson").Return()

	mockObject := new(MockGCSObjectHandle)
	mockObject.On("NewWriter", mock.Anything).Return(mockWriter)

	mockBucket := new(MockGCSBucketHandle)
	mockBucket.On("Object", mock.Anything).Return(mockObject)

	mockClient := new(MockGCSClient)
	mockClient.On("Bucket", "mock-bucket").Return(mockBucket)
	mockClient.On("Close").Return(nil)

	NewGCSClient = func(ctx context.Context) (GCSClient, error) {
		return mockClient, nil
	}

	buf, restoreLog := captureLog()
	defer restoreLog()

	event := TelemetryEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Event:     EventDeployStart,
		File:      "blueprint.yaml",
		Message:   "Deploying",
		Modules:   []string{"m1"},
	}

	sendToUserGCSBucket(context.Background(), event)

	mockClient.AssertExpectations(t)
	mockBucket.AssertExpectations(t)
	mockObject.AssertExpectations(t)
	mockWriter.AssertExpectations(t)

	assert.Contains(t, buf.String(), "Telemetry data successfully sent to user GCS bucket", "Expected success log message")

	var writtenEvent TelemetryEvent
	err := json.Unmarshal(bytes.TrimSpace(mockWriter.Buffer.Bytes()), &writtenEvent)
	assert.NoError(t, err)
	assert.Equal(t, event.Event, writtenEvent.Event)
	assert.Equal(t, event.File, writtenEvent.File)
	assert.Equal(t, "application/x-ndjson", mockWriter.ContentType, "ContentType should be set correctly on the mock writer")
}

func TestSendToUserGCSBucket_ClientCreationError(t *testing.T) {
	defer resetTelemetryState()

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_BUCKET", "mock-bucket")
	userTelemetryBucket = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")
	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_PROJECT", "test-project-id")
	userProjectID = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")

	expectedError := errors.New("failed to create GCS client")
	NewGCSClient = func(ctx context.Context) (GCSClient, error) {
		return nil, expectedError
	}

	buf, restoreLog := captureLog()
	defer restoreLog()

	event := TelemetryEvent{}
	sendToUserGCSBucket(context.Background(), event)

	assert.Contains(t, buf.String(), "telemetry warning: failed to create GCS client for user bucket: "+expectedError.Error())
}

func TestSendToUserGCSBucket_WriterCloseError(t *testing.T) {
	defer resetTelemetryState()

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_BUCKET", "mock-bucket")
	userTelemetryBucket = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_BUCKET")
	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_PROJECT", "test-project-id")
	userProjectID = os.Getenv("CLUSTER_TOOLKIT_TELEMETRY_PROJECT")

	mockWriter := new(MockGCSWriter)
	mockWriter.On("Write", mock.Anything).Return(0, nil)
	mockWriter.On("SetContentType", "application/x-ndjson").Return()
	expectedCloseError := errors.New("failed to close GCS writer")
	mockWriter.On("Close").Return(expectedCloseError)

	mockObject := new(MockGCSObjectHandle)
	mockObject.On("NewWriter", mock.Anything).Return(mockWriter)

	mockBucket := new(MockGCSBucketHandle)
	mockBucket.On("Object", mock.Anything).Return(mockObject)

	mockClient := new(MockGCSClient)
	mockClient.On("Bucket", "mock-bucket").Return(mockBucket)
	mockClient.On("Close").Return(nil)

	NewGCSClient = func(ctx context.Context) (GCSClient, error) {
		return mockClient, nil
	}

	buf, restoreLog := captureLog()
	defer restoreLog()

	event := TelemetryEvent{}
	sendToUserGCSBucket(context.Background(), event)

	mockClient.AssertExpectations(t)
	mockBucket.AssertExpectations(t)
	mockObject.AssertExpectations(t)
	mockWriter.AssertExpectations(t)

	assert.Contains(t, buf.String(), "telemetry warning: failed to close GCS writer for user bucket \"mock-bucket\": "+expectedCloseError.Error())
	assert.NotContains(t, buf.String(), "Telemetry data successfully sent to user GCS bucket", "Success message should not be logged on write error")
}

func TestSendMetricToCloudDashboard_SimulatedSuccess(t *testing.T) {
	defer resetTelemetryState()

	testCases := []struct {
		name              string
		event             EventType
		filePath          string
		modules           []string
		message           string
		expectedEvent     string
		expectedBlueprint string
		expectedStatus    string
		expectedModules   []string
	}{
		{
			name:              "DeploySuccess",
			event:             EventDeploySuccess,
			filePath:          "blueprint.yaml",
			modules:           []string{"module-x"},
			message:           "success",
			expectedEvent:     "deploy_success",
			expectedBlueprint: "blueprint.yaml",
			expectedStatus:    "success",
			expectedModules:   []string{"module-x"},
		},
		{
			name:              "CreateStart",
			event:             EventCreateStart,
			filePath:          "/path/to/my/blueprint.yaml",
			modules:           []string{"module-a", "module-b"},
			message:           "starting create",
			expectedEvent:     "create_start",
			expectedBlueprint: "blueprint.yaml",
			expectedStatus:    "starting create",
			expectedModules:   []string{"module-a", "module-b"},
		},
		{
			name:              "DestroyError",
			event:             EventDestroyError,
			filePath:          "failed_blueprint.yaml",
			modules:           []string{},
			message:           "failed destroy",
			expectedEvent:     "destroy_error",
			expectedBlueprint: "failed_blueprint.yaml",
			expectedStatus:    "failed destroy",
			expectedModules:   []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockMetricClient := new(MockCloudMonitoringClient)
			mockMetricClient.On("CreateTimeSeries", mock.Anything, mock.MatchedBy(func(req *monitoringpb.CreateTimeSeriesRequest) bool {
				assert.NotEmpty(t, req.Name)
				assert.Len(t, req.TimeSeries, len(tc.expectedModules)+1)

				blueprintTS := req.TimeSeries[0]
				assert.Equal(t, "custom.googleapis.com/cluster_toolkit/event_count", blueprintTS.Metric.Type)
				assert.Equal(t, map[string]string{"blueprint": tc.expectedBlueprint, "event": tc.expectedEvent, "status": tc.expectedStatus}, blueprintTS.Metric.Labels)
				assert.Equal(t, map[string]string{"project_id": "hpc-toolkit-gsc"}, blueprintTS.Resource.Labels)
				assert.Len(t, blueprintTS.Points, 1)
				assert.NotNil(t, blueprintTS.Points[0].Interval.EndTime)
				assert.Equal(t, int64(1), blueprintTS.Points[0].Value.GetInt64Value())

				for i, moduleName := range tc.expectedModules {
					moduleTS := req.TimeSeries[i+1]
					assert.Equal(t, "custom.googleapis.com/cluster_toolkit/event_count", moduleTS.Metric.Type)
					assert.Equal(t, map[string]string{"module": moduleName}, moduleTS.Metric.Labels)
					assert.Equal(t, map[string]string{"project_id": "hpc-toolkit-gsc"}, moduleTS.Resource.Labels)
					assert.Len(t, moduleTS.Points, 1)
					assert.NotNil(t, moduleTS.Points[0].Interval.EndTime)
					assert.Equal(t, int64(1), moduleTS.Points[0].Value.GetInt64Value())
				}

				return true
			})).Return(nil)
			mockMetricClient.On("Close").Return(nil)

			NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) {
				return mockMetricClient, nil
			}

			buf, restoreLog := captureLog()
			defer restoreLog()

			sendMetricToCloudDashboard(context.Background(), tc.event, tc.filePath, tc.modules, tc.message)
			mockMetricClient.AssertExpectations(t)
			assert.Contains(t, buf.String(), "Telemetry data sent successfully.\n", "Expected success log message")
		})
	}
}

func TestSendMetricToCloudDashboard_ClientCreationError(t *testing.T) {
	defer resetTelemetryState()

	expectedError := errors.New("failed to create monitoring client")
	NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) {
		return nil, expectedError
	}

	buf, restoreLog := captureLog()
	defer restoreLog()

	sendMetricToCloudDashboard(context.Background(), EventDeploySuccess, "bp.yaml", []string{}, "success")

	assert.Contains(t, buf.String(), "telemetry warning: failed to create monitoring client: "+expectedError.Error())
}

func TestSendMetricToCloudDashboard_CreateTimeSeriesError(t *testing.T) {
	defer resetTelemetryState()

	mockMetricClient := new(MockCloudMonitoringClient)
	expectedError := errors.New("failed to write time series data")
	mockMetricClient.On("CreateTimeSeries", mock.Anything, mock.Anything).Return(expectedError)
	mockMetricClient.On("Close").Return(nil)

	NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) {
		return mockMetricClient, nil
	}

	buf, restoreLog := captureLog()
	defer restoreLog()

	sendMetricToCloudDashboard(context.Background(), EventDeployError, "failed.yaml", []string{}, "error msg")

	mockMetricClient.AssertExpectations(t)
	assert.Contains(t, buf.String(), "telemetry warning: Failed to write time series data: "+expectedError.Error())
	assert.NotContains(t, buf.String(), "Telemetry data sent successfully.", "Success message should not be logged on write error")
}

func TestLogEvent_EndToEndWithMocks(t *testing.T) {
	defer resetTelemetryState()
	defer os.Remove("telemetry.log")

	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY", "true")
	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_BUCKET", "my-test-bucket")
	setEnv(t, "CLUSTER_TOOLKIT_TELEMETRY_PROJECT", "my-test-project")

	TelemetryEnabled = true
	userTelemetryBucket = "my-test-bucket"
	userProjectID = "my-test-project"

	mockGCSWriter := new(MockGCSWriter)
	mockGCSWriter.On("Write", mock.Anything).Return(0, nil)
	mockGCSWriter.On("Close").Return(nil)
	mockGCSWriter.On("SetContentType", "application/x-ndjson").Return()

	mockGCSObject := new(MockGCSObjectHandle)
	mockGCSObject.On("NewWriter", mock.Anything).Return(mockGCSWriter)

	mockGCSBucket := new(MockGCSBucketHandle)
	mockGCSBucket.On("Object", mock.Anything).Return(mockGCSObject)

	mockGCSClient := new(MockGCSClient)
	mockGCSClient.On("Bucket", "my-test-bucket").Return(mockGCSBucket)
	mockGCSClient.On("Close").Return(nil)

	mockMetricClient := new(MockCloudMonitoringClient)
	mockMetricClient.On("CreateTimeSeries", mock.Anything, mock.Anything).Return(nil)
	mockMetricClient.On("Close").Return(nil)

	NewGCSClient = func(ctx context.Context) (GCSClient, error) { return mockGCSClient, nil }
	NewMetricClient = func(ctx context.Context) (CloudMonitoringClient, error) { return mockMetricClient, nil }

	buf, restoreLog := captureLog()
	defer restoreLog()

	testFilePath := "some/path/to/blueprint.yaml"
	testModules := []string{"mod1", "mod2"}
	testMessage := "operation complete"
	LogEvent(EventCreateSuccess, testFilePath, testMessage, testModules)

	mockGCSClient.AssertExpectations(t)
	mockGCSBucket.AssertExpectations(t)
	mockGCSObject.AssertExpectations(t)
	mockGCSWriter.AssertExpectations(t)
	mockMetricClient.AssertExpectations(t)

	_, err := os.Stat("telemetry.log")
	assert.NoError(t, err, "telemetry.log file should exist")
	fileContent, _ := os.ReadFile("telemetry.log")
	var loggedEvent TelemetryEvent
	err = json.Unmarshal(bytes.TrimSpace(fileContent), &loggedEvent)
	assert.NoError(t, err)
	assert.Equal(t, EventCreateSuccess, loggedEvent.Event)
	assert.Equal(t, testFilePath, loggedEvent.File)
	assert.Equal(t, testMessage, loggedEvent.Message)
	assert.Equal(t, testModules, loggedEvent.Modules)

	var gcsLoggedEvent TelemetryEvent
	err = json.Unmarshal(bytes.TrimSpace(mockGCSWriter.Buffer.Bytes()), &gcsLoggedEvent)
	assert.NoError(t, err)
	assert.Equal(t, EventCreateSuccess, gcsLoggedEvent.Event)
	assert.Equal(t, "application/x-ndjson", mockGCSWriter.ContentType, "ContentType should be set correctly on the mock writer")

	logOutput := buf.String()
	assert.Contains(t, logOutput, "Telemetry data successfully sent to user GCS bucket \"my-test-bucket\"")
	assert.Contains(t, logOutput, "Telemetry data sent successfully.")
}

func TestMockGCSWriter_ConcurrentWrite(t *testing.T) {
	mockWriter := new(MockGCSWriter)
	mockWriter.On("Write", mock.Anything).Return(0, nil)

	var numGoroutines = 10
	var numWritesPerGoroutine = 10

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for j := 0; j < numWritesPerGoroutine; j++ {
				_, err := mockWriter.Write([]byte(fmt.Sprintf("Write from goroutine %d, write %d\n", index, j)))
				assert.NoError(t, err, "Write should not return error")
			}
		}(i)
	}

	wg.Wait()

	mockWriter.AssertExpectations(t)
	assert.NotNil(t, mockWriter.Buffer.Bytes(), "Buffer should not be nil")
	assert.NotEmpty(t, mockWriter.Buffer.Bytes(), "Buffer should not be empty")
}

type mockFileReader struct {
	content []byte
	err     error
}

func (m *mockFileReader) ReadFile(name string) ([]byte, error) {
	return m.content, m.err
}

var originalReadFile = os.ReadFile

func setMockReadFile(content []byte, err error) {
	osReadFileFunc = func(name string) ([]byte, error) {
		return content, err
	}
}

func restoreReadFile() {
	osReadFileFunc = originalReadFile
}
