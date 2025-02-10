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

func matchMetricExact(exp struct {
	name   string
	labels map[string]string
	value  float64
}, metric prometheus.Metric, dtoMetric *io_prometheus_client.Metric) bool {
	if !strings.Contains(metric.Desc().String(), exp.name) {
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

func matchMetricDuration(exp struct {
	name   string
	labels map[string]string
}, metric prometheus.Metric, dtoMetric *io_prometheus_client.Metric) bool {
	if !strings.Contains(metric.Desc().String(), exp.name) {
		return false
	}

	for _, label := range dtoMetric.GetLabel() {
		if val, ok := exp.labels[label.GetName()]; !ok || val != label.GetValue() {
			return false
		}
	}

	return *dtoMetric.Gauge.Value > 0
}

func TestS3Collector(t *testing.T) {
	s3Endpoint = "http://localhost"
	s3Region = "us-east-1"

	metricsMutex.Lock()
	cachedMetrics = controllers.S3Summary{
		S3Status: true,
		StorageClasses: map[string]controllers.StorageClassMetrics{
			"STANDARD": {
				Size:         1000.0,
				ObjectNumber: 50.0,
			},
		},
		TotalListDuration: 2 * time.Second,
		S3Buckets: []controllers.Bucket{
			{
				BucketName: "test-bucket",
				StorageClasses: map[string]controllers.StorageClassMetrics{
					"STANDARD": {
						Size:         500.0,
						ObjectNumber: 25.0,
					},
				},
				ListDuration: 1 * time.Second,
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
		expectedExact := []struct {
			name   string
			labels map[string]string
			value  float64
		}{
			{"s3_endpoint_up", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 1.0},
			{"s3_total_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "storageClass": "STANDARD"}, 1000.0},
			{"s3_total_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "storageClass": "STANDARD"}, 50.0},
			{"s3_bucket_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket", "storageClass": "STANDARD"}, 500.0},
			{"s3_bucket_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket", "storageClass": "STANDARD"}, 25.0},
		}

		expectedDuration := []struct {
			name   string
			labels map[string]string
		}{
			{"s3_list_total_duration_seconds", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}},
			{"s3_list_duration_seconds", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket"}},
		}

		var matchedExactCount int
		var matchedDurationCount int

		for metric := range ch {
			metrics = append(metrics, metric)

			dtoMetric := &io_prometheus_client.Metric{}
			err := metric.Write(dtoMetric)
			require.NoError(t, err)

			for _, exp := range expectedExact {
				if matchMetricExact(exp, metric, dtoMetric) {
					matchedExactCount++
					break
				}
			}

			for _, exp := range expectedDuration {
				if matchMetricDuration(exp, metric, dtoMetric) {
					matchedDurationCount++
					break
				}
			}
		}

		assert.Equal(t, len(expectedExact), matchedExactCount, "Not all expected exact metrics were found")
		assert.Equal(t, len(expectedDuration), matchedDurationCount, "Not all expected duration metrics were found")
		assert.Equal(t, len(expectedExact)+len(expectedDuration), len(metrics), "Mismatch in number of metrics")
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
						Key:          aws.String("test-object"),
						Size:         aws.Int64(1024),
						StorageClass: types.ObjectStorageClass("STANDARD"),
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

	assert.NoError(t, cachedError, "Expected no error with mock client")
	assert.Equal(t, true, cachedMetrics.S3Status, "S3Status should be true")
	metrics := cachedMetrics.StorageClasses["STANDARD"]
	assert.Equal(t, 1024.0, metrics.Size, "Total size should match")
	assert.Equal(t, 1.0, metrics.ObjectNumber, "Total object number should match")
	require.Len(t, cachedMetrics.S3Buckets, 1, "Should have exactly one bucket")

	bucket := cachedMetrics.S3Buckets[0]
	assert.Equal(t, "test-bucket", bucket.BucketName, "BucketName should match")
	bucketMetrics := bucket.StorageClasses["STANDARD"]
	assert.Equal(t, 1024.0, bucketMetrics.Size, "Bucket size should match")
	assert.Equal(t, 1.0, bucketMetrics.ObjectNumber, "Bucket object number should match")
	assert.Greater(t, cachedMetrics.TotalListDuration, time.Duration(0), "TotalListDuration should be positive")
	assert.Greater(t, bucket.ListDuration, time.Duration(0), "Bucket ListDuration should be positive")
}
