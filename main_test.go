package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tropnikovvl/s3-bucket-exporter/controllers"
)

// Mock implementation for S3 client interface
type mockS3Client struct {
	controllers.S3ClientInterface
	listBucketsFunc   func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	listObjectsV2Func func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

func (m *mockS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return m.listBucketsFunc(ctx, params, optFns...)
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return m.listObjectsV2Func(ctx, params, optFns...)
}

func TestEnvString(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		defValue string
		envValue string
		expValue string
	}{
		{
			name:     "returns default when env not set",
			key:      "TEST_KEY_1",
			defValue: "default",
			envValue: "",
			expValue: "default",
		},
		{
			name:     "returns env value when set",
			key:      "TEST_KEY_2",
			defValue: "default",
			envValue: "from_env",
			expValue: "from_env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}
			got := envString(tt.key, tt.defValue)
			assert.Equal(t, tt.expValue, got)
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		defValue bool
		envValue string
		expValue bool
	}{
		{
			name:     "returns default when env not set",
			key:      "TEST_BOOL_1",
			defValue: false,
			envValue: "",
			expValue: false,
		},
		{
			name:     "returns true when env is 'true'",
			key:      "TEST_BOOL_2",
			defValue: false,
			envValue: "true",
			expValue: true,
		},
		{
			name:     "returns default for invalid value",
			key:      "TEST_BOOL_3",
			defValue: true,
			envValue: "invalid",
			expValue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}
			got := envBool(tt.key, tt.defValue)
			assert.Equal(t, tt.expValue, got)
		})
	}
}

func TestHealthHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/health", nil)
	assert.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(healthHandler)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func matchMetric(exp struct {
	name   string
	labels map[string]string
	value  float64
}, metric prometheus.Metric, dtoMetric *io_prometheus_client.Metric) bool {
	desc := metric.Desc().String()
	if !strings.Contains(desc, exp.name) {
		return false
	}
	for _, label := range dtoMetric.GetLabel() {
		if val, ok := exp.labels[label.GetName()]; !ok || val != label.GetValue() {
			return false
		}
	}
	if dtoMetric.GetGauge() != nil {
		return dtoMetric.GetGauge().GetValue() == exp.value
	}
	return false
}

func TestS3Collector(t *testing.T) {
	s3Endpoint = "http://localhost"
	s3Region = "us-east-1"

	metricsMutex.Lock()
	cachedMetrics = controllers.S3Summary{
		S3Status:       true,
		S3Size:         1000.0,
		S3ObjectNumber: 50.0,
		S3Buckets: []controllers.Bucket{
			{
				BucketName:         "test-bucket",
				BucketSize:         500.0,
				BucketObjectNumber: 25.0,
			},
		},
	}
	cachedError = nil
	metricsMutex.Unlock()

	collector := S3Collector{}
	ch := make(chan prometheus.Metric)
	done := make(chan bool)

	var metrics []prometheus.Metric

	go func() {
		expected := []struct {
			name   string
			labels map[string]string
			value  float64
		}{
			{"s3_endpoint_up", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 1.0},
			{"s3_total_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 1000.0},
			{"s3_total_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 50.0},
			{"s3_bucket_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket"}, 500.0},
			{"s3_bucket_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket"}, 25.0},
		}

		var matchedCount int

		for metric := range ch {
			metrics = append(metrics, metric)

			dtoMetric := &io_prometheus_client.Metric{}
			err := metric.Write(dtoMetric)
			require.NoError(t, err)

			for _, exp := range expected {
				if matchMetric(exp, metric, dtoMetric) {
					matchedCount++
					break
				}
			}
		}

		assert.Equal(t, len(expected), matchedCount, "Not all expected metrics were found")
		assert.Equal(t, len(expected), len(metrics), "Mismatch in number of metrics")
		done <- true
	}()

	collector.Collect(ch)
	close(ch)
	<-done
}

func TestUpdateMetrics(t *testing.T) {
	mockClient := &mockS3Client{
		listBucketsFunc: func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
			return &s3.ListBucketsOutput{
				Buckets: []types.Bucket{
					{Name: aws.String("test-bucket")},
				},
			}, nil
		},
		listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []types.Object{
					{
						Key:  aws.String("test-object"),
						Size: aws.Int64(1024),
					},
				},
				IsTruncated: aws.Bool(false),
			}, nil
		},
	}

	s3Endpoint = "http://localhost"
	s3AccessKey = "test"
	s3SecretKey = "test"
	s3Region = "us-east-1"
	s3BucketNames = "test-bucket"

	controllers.SetS3Client(mockClient)
	defer controllers.ResetS3Client()

	interval := 100 * time.Millisecond
	done := make(chan bool)

	go func() {
		updateMetrics(interval)
	}()

	go func() {
		time.Sleep(interval * 2)
		done <- true
	}()

	<-done

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()

	expectedMetrics := controllers.S3Summary{
		S3Status:       true,
		S3Size:         1024.0,
		S3ObjectNumber: 1.0,
		S3Buckets: []controllers.Bucket{
			{
				BucketName:         "test-bucket",
				BucketSize:         1024.0,
				BucketObjectNumber: 1.0,
			},
		},
	}

	assert.NoError(t, cachedError, "Expected no error with mock client")
	assert.Equal(t, expectedMetrics, cachedMetrics, "Metrics must be equal")
}
