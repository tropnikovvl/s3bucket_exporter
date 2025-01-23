package controllers

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
)

// Bucket - information per bucket
type Bucket struct {
	BucketName         string  `json:"bucketName"`
	BucketSize         float64 `json:"bucketSize"`
	BucketObjectNumber float64 `json:"bucketObjectNumber"`
}

// Buckets - list of Bucket objects
type Buckets []Bucket

// S3Summary - one JSON struct to rule them all
type S3Summary struct {
	S3Status       bool    `json:"s3Status"`
	S3Size         float64 `json:"s3Size"`
	S3ObjectNumber float64 `json:"s3ObjectNumber"`
	S3Buckets      Buckets `json:"s3Buckets"`
}

// S3Conn struct - keeps information about remote S3
type S3Conn struct {
	S3ConnAccessKey      string `json:"s3_conn_access_key"`
	S3ConnSecretKey      string `json:"s3_conn_secret_key"`
	S3ConnEndpoint       string `json:"s3_conn_endpoint,omitempty"`
	S3ConnRegion         string `json:"s3_conn_region"`
	S3ConnForcePathStyle bool   `json:"s3_conn_force_path_style"`
}

type S3ClientInterface interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

var s3ClientInstance S3ClientInterface

// SetS3Client sets the S3 client instance for testing
func SetS3Client(client S3ClientInterface) {
	s3ClientInstance = client
}

// ResetS3Client resets the S3 client instance
func ResetS3Client() {
	s3ClientInstance = nil
}

// getS3Client returns the S3 client instance or creates a new one
func getS3Client(cfg aws.Config, s3Conn S3Conn) S3ClientInterface {
	if s3ClientInstance != nil {
		return s3ClientInstance
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if s3Conn.S3ConnEndpoint != "" {
			o.BaseEndpoint = aws.String(s3Conn.S3ConnEndpoint)
		}
		o.Credentials = credentials.NewStaticCredentialsProvider(s3Conn.S3ConnAccessKey, s3Conn.S3ConnSecretKey, "")
		o.UsePathStyle = s3Conn.S3ConnForcePathStyle
	})
}

// S3UsageInfo - gets S3 connection details and returns S3Summary
func S3UsageInfo(s3Conn S3Conn, s3BucketNames string) (S3Summary, error) {
	summary := S3Summary{S3Status: false}

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(s3Conn.S3ConnRegion))
	if err != nil {
		log.Errorf("Failed to create AWS session: %v", err)
		return summary, err
	}

	s3Client := getS3Client(cfg, s3Conn)

	return fetchBucketData(s3BucketNames, s3Client, s3Conn.S3ConnRegion, summary)
}

func fetchBucketData(s3BucketNames string, s3Client S3ClientInterface, s3Region string, summary S3Summary) (S3Summary, error) {
	// checkSingleBucket - retrieves data for a specific buckets
	if s3BucketNames != "" {
		buckets := strings.Split(s3BucketNames, ",")
		log.Debugf("List of buckets in %s region: %s", s3Region, buckets)
		var err error
		for _, bucketName := range buckets {
			bucketName = strings.TrimSpace(bucketName)
			if bucketName != "" {
				if summary, err = processBucket(bucketName, s3Client, summary); err != nil {
					log.Errorf("Failed to process bucket %s: %v", bucketName, err)
				}
			}
		}
		return summary, nil
	}

	// checkAllBuckets - retrieves data for all available buckets
	result, err := s3Client.ListBuckets(context.TODO(), &s3.ListBucketsInput{BucketRegion: aws.String(s3Region)})
	if err != nil {
		log.Errorf("Failed to list buckets: %v", err)
		return summary, errors.New("unable to connect to S3 endpoint")
	}

	var debugListBuckets []string
	for _, bucket := range result.Buckets {
		debugListBuckets = append(debugListBuckets, aws.ToString(bucket.Name))
	}
	log.Debugf("List of buckets in %s region: %v", s3Region, debugListBuckets)

	for _, b := range result.Buckets {
		if summary, err = processBucket(*b.Name, s3Client, summary); err != nil {
			log.Errorf("Failed to process bucket %s: %v", *b.Name, err)
			continue
		}
	}

	return summary, nil
}

func processBucket(bucketName string, s3Client S3ClientInterface, summary S3Summary) (S3Summary, error) {
	size, count, err := calculateBucketMetrics(bucketName, s3Client)
	if err != nil {
		log.Errorf("Failed to get metrics for bucket %s: %v", bucketName, err)
		return summary, err
	}

	bucket := Bucket{
		BucketName:         bucketName,
		BucketSize:         size,
		BucketObjectNumber: count,
	}
	log.Debugf("Bucket size and objects count: %v", bucket)
	summary.S3Buckets = append(summary.S3Buckets, bucket)
	summary.S3Size += size
	summary.S3ObjectNumber += count
	summary.S3Status = true

	return summary, nil
}

// calculateBucketMetrics - computes the total size and object count for a bucket
func calculateBucketMetrics(bucketName string, s3Client S3ClientInterface) (float64, float64, error) {
	var totalSize, objectCount float64
	var continuationToken *string

	for {
		page, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			log.Errorf("Failed to list objects for bucket %s: %v", bucketName, err)
			return 0, 0, err
		}

		for _, obj := range page.Contents {
			totalSize += float64(*obj.Size)
			objectCount++
		}

		if page.IsTruncated != nil && !*page.IsTruncated {
			break
		}
		continuationToken = page.NextContinuationToken
	}

	return totalSize, objectCount, nil
}
