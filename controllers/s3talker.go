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

// StorageClassMetrics
type StorageClassMetrics struct {
	Size         float64 `json:"size"`
	ObjectNumber float64 `json:"objectNumber"`
}

// Bucket - information per bucket
type Bucket struct {
	BucketName     string                         `json:"bucketName"`
	StorageClasses map[string]StorageClassMetrics `json:"storageClasses"`
	ListDuration   time.Duration                  `json:"listDuration"`
}

// Buckets - list of Bucket objects
type Buckets []Bucket

// S3Summary - one JSON struct to rule them all
type S3Summary struct {
	S3Status          bool                           `json:"s3Status"`
	StorageClasses    map[string]StorageClassMetrics `json:"storageClasses"`
	S3Buckets         Buckets                        `json:"s3Buckets"`
	TotalListDuration time.Duration                  `json:"totalListDuration"`
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

// distinct - removes duplicates from a slice of strings
func distinct(input []string) []string {
	seen := make(map[string]struct{})
	result := []string{}

	for _, val := range input {
		val = strings.TrimSpace(val)
		if val != "" {
			if _, exists := seen[val]; !exists {
				seen[val] = struct{}{}
				result = append(result, val)
			}
		}
	}

	return result
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
		bucketNames = distinct(strings.Split(s3BucketNames, ","))
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

	var wg sync.WaitGroup
	var summaryMutex sync.Mutex

	summaryMutex.Lock()
	summary.StorageClasses = make(map[string]StorageClassMetrics)
	summary.S3Buckets = make(Buckets, 0, len(bucketNames))
	summaryMutex.Unlock()

	processBucketResult := func(bucket Bucket) {
		summaryMutex.Lock()
		defer summaryMutex.Unlock()

		summary.S3Buckets = append(summary.S3Buckets, bucket)
		for storageClass, metrics := range bucket.StorageClasses {
			summaryMetrics := summary.StorageClasses[storageClass]
			summaryMetrics.Size += metrics.Size
			summaryMetrics.ObjectNumber += metrics.ObjectNumber
			summary.StorageClasses[storageClass] = summaryMetrics
		}
		log.Debugf("Bucket size and objects count: %v", bucket)
	}

	var errs []error
	var errMutex sync.Mutex

	for _, bucketName := range bucketNames {
		bucketName := strings.TrimSpace(bucketName)
		if bucketName == "" {
			continue
		}

		wg.Add(1)
		go func(bucketName string) {
			defer wg.Done()

			storageClasses, duration, err := calculateBucketMetrics(bucketName, s3Client)
			if err != nil {
				errMutex.Lock()
				errs = append(errs, err)
				errMutex.Unlock()
				return
			}

			bucket := Bucket{
				BucketName:     bucketName,
				StorageClasses: storageClasses,
				ListDuration:   duration,
			}

			processBucketResult(bucket)
			log.Debugf("Finish bucket %s processing", bucketName)
		}(bucketName)
	}

	wg.Wait()

	if len(errs) > 0 {
		log.Errorf("Encountered errors while processing buckets: %v", errs)
	}

	if len(summary.S3Buckets) > 0 {
		summary.S3Status = true
	}

	summary.TotalListDuration = time.Since(start)
	return summary, nil
}

// calculateBucketMetrics - computes the total size and object count for a bucket
func calculateBucketMetrics(bucketName string, s3Client S3ClientInterface) (map[string]StorageClassMetrics, time.Duration, error) {
	var continuationToken *string
	storageClasses := make(map[string]StorageClassMetrics)

	start := time.Now()

	for {
		page, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			log.Errorf("Failed to list objects for bucket %s: %v", bucketName, err)
			return nil, 0, err
		}

		for _, obj := range page.Contents {
			storageClass := string(obj.StorageClass)
			if storageClass == "" {
				storageClass = "STANDARD"
			}

			metrics := storageClasses[storageClass]
			metrics.Size += float64(*obj.Size)
			metrics.ObjectNumber++
			storageClasses[storageClass] = metrics
		}

		if page.IsTruncated != nil && !*page.IsTruncated {
			break
		}
		continuationToken = page.NextContinuationToken
	}

	duration := time.Since(start)
	return storageClasses, duration, nil
}
