package controllers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
)

// Bucket - information per bucket
type Bucket struct {
	BucketName         string        `json:"bucketName"`
	BucketSize         float64       `json:"bucketSize"`
	BucketObjectNumber float64       `json:"bucketObjectNumber"`
	ListDuration       time.Duration `json:"listDuration"`
}

// Buckets - list of Bucket objects
type Buckets []Bucket

// S3Summary - one JSON struct to rule them all
type S3Summary struct {
	S3Status          bool          `json:"s3Status"`
	S3Size            float64       `json:"s3Size"`
	S3ObjectNumber    float64       `json:"s3ObjectNumber"`
	S3Buckets         Buckets       `json:"s3Buckets"`
	TotalListDuration time.Duration `json:"totalListDuration"`
}

// S3Conn struct - keeps information about remote S3
type S3Conn struct {
	S3ConnAccessKey      string `json:"s3_conn_access_key"`
	S3ConnSecretKey      string `json:"s3_conn_secret_key"`
	S3ConnEndpoint       string `json:"s3_conn_endpoint,omitempty"`
	S3ConnRegion         string `json:"s3_conn_region"`
	S3ConnForcePathStyle bool   `json:"s3_conn_force_path_style"`
	UseIAMRole           bool   `json:"use_iam_role"`
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

	options := func(o *s3.Options) {
		if s3Conn.S3ConnEndpoint != "" {
			o.BaseEndpoint = aws.String(s3Conn.S3ConnEndpoint)
		}
		if !s3Conn.UseIAMRole {
			// Only set static credentials if explicitly not using IAM role
			o.Credentials = credentials.NewStaticCredentialsProvider(
				s3Conn.S3ConnAccessKey,
				s3Conn.S3ConnSecretKey,
				"",
			)
		}
		o.UsePathStyle = s3Conn.S3ConnForcePathStyle
	}

	return s3.NewFromConfig(cfg, options)
}

// S3UsageInfo - gets S3 connection details and returns S3Summary
func S3UsageInfo(s3Conn S3Conn, s3BucketNames string) (S3Summary, error) {
	summary := S3Summary{S3Status: false}

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(s3Conn.S3ConnRegion))
	if err != nil {
		log.Errorf("Failed to create AWS config: %v", err)
		return summary, err
	}

	s3Client := getS3Client(cfg, s3Conn)

	return fetchBucketData(s3BucketNames, s3Client, s3Conn.S3ConnRegion, summary)
}

func fetchBucketData(s3BucketNames string, s3Client S3ClientInterface, s3Region string, summary S3Summary) (S3Summary, error) {
	var bucketNames []string
	start := time.Now()

	if s3BucketNames != "" {
		// If specific buckets are provided, use them
		bucketNames = strings.Split(s3BucketNames, ",")
	} else {
		// Otherwise, fetch all buckets
		result, err := s3Client.ListBuckets(context.TODO(), &s3.ListBucketsInput{BucketRegion: aws.String(s3Region)})
		if err != nil {
			log.Errorf("Failed to list buckets: %v", err)
			return summary, errors.New("unable to connect to S3 endpoint")
		}

		for _, b := range result.Buckets {
			bucketNames = append(bucketNames, aws.ToString(b.Name))
		}
	}

	log.Debugf("List of buckets in %s region: %v", s3Region, bucketNames)

	resultsChan := make(chan Bucket, len(bucketNames))
	errorChan := make(chan error, len(bucketNames))

	var wg sync.WaitGroup

	for _, bucketName := range bucketNames {
		bucketName := strings.TrimSpace(bucketName)
		if bucketName == "" {
			continue
		}

		wg.Add(1)
		go func(bucketName string) {
			defer wg.Done()

			size, count, duration, err := calculateBucketMetrics(bucketName, s3Client)
			if err != nil {
				errorChan <- err
				return
			}

			resultsChan <- Bucket{
				BucketName:         bucketName,
				BucketSize:         size,
				BucketObjectNumber: count,
				ListDuration:       duration,
			}
			log.Debugf("Finish bucket %s processing", bucketName)
		}(bucketName)
	}

	wg.Wait()
	close(resultsChan)
	close(errorChan)

	var errs []error
	for err := range errorChan {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		log.Errorf("Encountered errors while processing buckets: %v", errs)
	}

	for bucket := range resultsChan {
		summary.S3Buckets = append(summary.S3Buckets, bucket)
		summary.S3Size += bucket.BucketSize
		summary.S3ObjectNumber += bucket.BucketObjectNumber
		log.Debugf("Bucket size and objects count: %v", bucket)
	}

	if len(summary.S3Buckets) > 0 {
		summary.S3Status = true
	}

	summary.TotalListDuration = time.Since(start)
	return summary, nil
}

// calculateBucketMetrics - computes the total size and object count for a bucket
func calculateBucketMetrics(bucketName string, s3Client S3ClientInterface) (float64, float64, time.Duration, error) {
	var totalSize, objectCount float64
	var continuationToken *string

	start := time.Now()

	for {
		page, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			log.Errorf("Failed to list objects for bucket %s: %v", bucketName, err)
			return 0, 0, 0, err
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

	duration := time.Since(start)
	return totalSize, objectCount, duration, nil
}
