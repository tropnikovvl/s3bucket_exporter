package controllers

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockS3Client implements S3ClientInterface
type MockS3Client struct {
	mock.Mock
}

func (m *MockS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*s3.ListBucketsOutput), args.Error(1)
}

func (m *MockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*s3.ListObjectsV2Output), args.Error(1)
}

func TestS3UsageInfo_SingleBucket(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		S3ConnRegion:    "us-west-2",
		S3ConnAccessKey: "test-key",
		S3ConnSecretKey: "test-secret",
	}

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.S3Status)
	assert.Equal(t, float64(3072), summary.S3Size)
	assert.Equal(t, float64(2), summary.S3ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestS3UsageInfo_MultipleBuckets(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		S3ConnRegion:    "us-west-2",
		S3ConnAccessKey: "test-key",
		S3ConnSecretKey: "test-secret",
	}

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1,bucket2")

	assert.NoError(t, err)
	assert.True(t, summary.S3Status)
	assert.Equal(t, float64(6144), summary.S3Size)
	assert.Equal(t, float64(4), summary.S3ObjectNumber)
	assert.Len(t, summary.S3Buckets, 2)
}

func TestS3UsageInfo_EmptyBucketList(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		S3ConnRegion:    "us-west-2",
		S3ConnAccessKey: "test-key",
		S3ConnSecretKey: "test-secret",
	}

	mockBucket1 := types.Bucket{Name: aws.String("bucket1")}
	mockBucket2 := types.Bucket{Name: aws.String("bucket2")}
	mockBucket3 := types.Bucket{Name: aws.String("bucket3")}

	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListBucketsOutput{
		Buckets: []types.Bucket{mockBucket1, mockBucket2, mockBucket3},
	}, nil)

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "")

	assert.NoError(t, err)
	assert.True(t, summary.S3Status)
	assert.Equal(t, float64(9216), summary.S3Size)
	assert.Equal(t, float64(6), summary.S3ObjectNumber)
	assert.Len(t, summary.S3Buckets, 3)
}

func TestCalculateBucketMetrics(t *testing.T) {
	mockClient := new(MockS3Client)

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
			{Size: aws.Int64(4096)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	size, count, err := calculateBucketMetrics("bucket1", mockClient)

	assert.NoError(t, err)
	assert.Equal(t, float64(7168), size)
	assert.Equal(t, float64(3), count)
}

func TestS3UsageInfo_WithIAMRole(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		S3ConnRegion:   "us-east-1",
		S3ConnEndpoint: "s3.amazonaws.com",
		UseIAMRole:     true,
	}

	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListBucketsOutput{
		Buckets: []types.Bucket{
			{Name: aws.String("bucket1")},
		},
	}, nil)

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(100)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.S3Status)
	assert.Equal(t, float64(100), summary.S3Size)
	assert.Equal(t, float64(1), summary.S3ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}
