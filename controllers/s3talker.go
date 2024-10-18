package controllers

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
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
	S3ConnQuota                     int64  `json:"s3_conn_quota,omitempty"`
	S3ConnAccessKey                 string `json:"s3_conn_access_key"`
	S3ConnSecretKey                 string `json:"s3_conn_secret_key"`
	S3ConnEndpoint                  string `json:"s3_conn_endpoint,omitempty"`
	S3ConnRegion                    string `json:"s3_conn_region"`
	S3ConnDisableSsl                bool   `json:"s3_conn_disable_ssl"`
	S3ConnForcePathStyle            bool   `json:"s3_conn_force_path_style"`
	S3ConnDisableEndpointHostPrefix bool   `json:"s3_conn_disable_endpoint_host_prefix"`
}

// S3UsageInfo - gets s3 connection details return s3Summary
func S3UsageInfo(s3Conn S3Conn, s3BucketName string) (S3Summary, error) {
	summary := S3Summary{S3Status: false}
	s3Config := &aws.Config{
		Credentials:               credentials.NewStaticCredentials(s3Conn.S3ConnAccessKey, s3Conn.S3ConnSecretKey, ""),
		Endpoint:                  aws.String(s3Conn.S3ConnEndpoint),
		DisableSSL:                aws.Bool(s3Conn.S3ConnDisableSsl),
		DisableEndpointHostPrefix: aws.Bool(s3Conn.S3ConnDisableEndpointHostPrefix),
		S3ForcePathStyle:          aws.Bool(s3Conn.S3ConnForcePathStyle),
		Region:                    aws.String(s3Conn.S3ConnRegion),
	}

	sess, err := session.NewSession(s3Config)
	if err != nil {
		log.Errorf("Failed to create AWS session: %v", err)
		return summary, err
	}

	s3Client := s3.New(sess)
	return fetchBucketData(s3BucketName, s3Client, summary)
}

func fetchBucketData(s3BucketName string, s3Client *s3.S3, summary S3Summary) (S3Summary, error) {
	// checkSingleBucket - retrieves data for a specific bucket
	if s3BucketName != "" {
		return processBucket(s3BucketName, s3Client, summary)
	}

	// checkAllBuckets - retrieves data for all available buckets
	result, err := s3Client.ListBuckets(nil)
	if err != nil {
		log.Errorf("Failed to list buckets: %v", err)
		return summary, errors.New("unable to connect to S3 endpoint")
	}

	for _, b := range result.Buckets {
		if summary, err = processBucket(aws.StringValue(b.Name), s3Client, summary); err != nil {
			log.Errorf("Failed to process bucket %s: %v", aws.StringValue(b.Name), err)
			continue
		}
	}

	return summary, nil
}

// processBucket retrieves size and object count metrics for a specific bucket.
func processBucket(bucketName string, s3Client *s3.S3, summary S3Summary) (S3Summary, error) {
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
	summary.S3Buckets = append(summary.S3Buckets, bucket)
	summary.S3Size += size
	summary.S3ObjectNumber += count
	summary.S3Status = true

	return summary, nil
}

// calculateBucketMetrics computes the total size and object count for a bucket.
func calculateBucketMetrics(bucketName string, s3Client *s3.S3) (float64, float64, error) {
	var totalSize, objectCount float64

	err := s3Client.ListObjectsV2Pages(&s3.ListObjectsV2Input{Bucket: aws.String(bucketName)},
		func(page *s3.ListObjectsV2Output, lastPage bool) bool {
			for _, obj := range page.Contents {
				totalSize += float64(*obj.Size)
				objectCount++
			}
			return !lastPage
		})

	if err != nil {
		log.Errorf("Failed to list objects for bucket %s: %v", bucketName, err)
		return 0, 0, err
	}
	return totalSize, objectCount, nil
}
