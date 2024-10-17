package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	S3Buckets      Buckets `json:"s3Bucket"`
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
	summary := S3Summary{}

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
		return summary, fmt.Errorf("failed to create AWS session: %w", err)
	}

	s3Client := s3.New(sess)

	return checkBuckets(s3BucketName, s3Client, summary)

}

func checkBuckets(s3BucketName string, s3Client *s3.S3, summary S3Summary) (S3Summary, error) {
	var err error

	// checkSingleBucket - retrieves data for a specific bucket
	if s3BucketName != "" {
		summary, err = processBucket(s3BucketName, s3Client, summary)
		if err != nil {
			summary.S3Status = false
			log.Errorf("Failed to get metrics for bucket %s: %v", s3BucketName, err)
		} else {
			summary.S3Status = true
		}
		return saveSummary(summary)
	}

	// checkAllBuckets - retrieves data for all available buckets
	result, err := s3Client.ListBuckets(nil)
	if err != nil {
		log.Errorln("Connection to S3 endpoint failed:", err)
		summary.S3Status = false
		return summary, errors.New("s3 endpoint: unable to connect")
	} else {
		summary.S3Status = true
	}

	// Calculate data for each bucket
	for _, b := range result.Buckets {
		summary, err = processBucket(aws.StringValue(b.Name), s3Client, summary)
		if err != nil {
			log.Errorf("Failed to get metrics for bucket %s: %v", aws.StringValue(b.Name), err)
			continue
		}
	}

	return saveSummary(summary)
}

func processBucket(bucketName string, s3Client *s3.S3, summary S3Summary) (S3Summary, error) {
	size, number, err := countBucketSize(bucketName, s3Client)
	if err != nil {
		return summary, fmt.Errorf("failed to get metrics for bucket %s: %w", bucketName, err)
	}
	bucket := Bucket{
		BucketName:         bucketName,
		BucketSize:         size,
		BucketObjectNumber: number,
	}
	summary.S3Buckets = append(summary.S3Buckets, bucket)
	summary.S3Size += size
	summary.S3ObjectNumber += number

	return summary, nil
}

// saveSummary - saves the S3 summary to a JSON file
func saveSummary(summary S3Summary) (S3Summary, error) {
	byteArray, err := json.MarshalIndent(summary, "", "    ")
	if err != nil {
		return summary, fmt.Errorf("failed to marshal S3 summary to JSON: %w", err)
	}
	if err := os.WriteFile("s3Information.json", byteArray, 0777); err != nil {
		return summary, fmt.Errorf("failed to write S3 summary to file: %w", err)
	}
	return summary, nil
}

// countBucketSize - calculates the size and number of objects in a bucket
func countBucketSize(bucketName string, s3Client *s3.S3) (float64, float64, error) {
	var bucketUsage, bucketObjects float64

	err := s3Client.ListObjectsV2Pages(&s3.ListObjectsV2Input{Bucket: aws.String(bucketName)},
		func(p *s3.ListObjectsV2Output, _ bool) bool {
			for _, obj := range p.Contents {
				bucketUsage += float64(*obj.Size)
				bucketObjects++
			}
			return true
		})

	if err != nil {
		return 0, 0, fmt.Errorf("failed to list objects for bucket %s: %w", bucketName, err)
	}
	return bucketUsage, bucketObjects, nil
}
