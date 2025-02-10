package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/tropnikovvl/s3-bucket-exporter/auth"
	"github.com/tropnikovvl/s3-bucket-exporter/controllers"
)

var (
	up = prometheus.NewDesc("s3_endpoint_up", "Connection to S3 successful", []string{"s3Endpoint", "s3Region"}, nil)

	listenPort       string
	logLevel         string
	logFormat        string
	scrapeInterval   string
	s3Endpoint       string
	s3BucketNames    string
	s3AccessKey      string
	s3SecretKey      string
	s3Region         string
	s3ForcePathStyle bool
	s3SkipTLSVerify  bool

	metricsMutex  sync.RWMutex
	cachedMetrics controllers.S3Summary
	cachedError   error
)

func envString(key, def string) string {
	if x := os.Getenv(key); x != "" {
		return x
	}
	return def
}

func envBool(key string, def bool) bool {
	def2, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return def2
}

func initFlags() {
	flag.StringVar(&s3Endpoint, "s3_endpoint", envString("S3_ENDPOINT", ""), "S3_ENDPOINT - eg. myceph.com:7480")
	flag.StringVar(&s3AccessKey, "s3_access_key", envString("S3_ACCESS_KEY", ""), "S3_ACCESS_KEY - aws_access_key")
	flag.StringVar(&s3SecretKey, "s3_secret_key", envString("S3_SECRET_KEY", ""), "S3_SECRET_KEY - aws_secret_key")
	flag.StringVar(&s3BucketNames, "s3_bucket_names", envString("S3_BUCKET_NAMES", ""), "S3_BUCKET_NAMES")
	flag.StringVar(&s3Region, "s3_region", envString("S3_REGION", "us-east-1"), "S3_REGION")
	flag.StringVar(&listenPort, "listen_port", envString("LISTEN_PORT", ":9655"), "LISTEN_PORT e.g ':9655'")
	flag.StringVar(&logLevel, "log_level", envString("LOG_LEVEL", "info"), "LOG_LEVEL")
	flag.StringVar(&logFormat, "log_format", envString("LOG_FORMAT", "text"), "LOG_FORMAT - 'text' or 'json'")
	flag.StringVar(&scrapeInterval, "scrape_interval", envString("SCRAPE_INTERVAL", "5m"), "SCRAPE_INTERVAL - eg. 30s, 5m, 1h")
	flag.BoolVar(&s3ForcePathStyle, "s3_force_path_style", envBool("S3_FORCE_PATH_STYLE", false), "S3_FORCE_PATH_STYLE")
	flag.BoolVar(&s3SkipTLSVerify, "s3_skip_tls_verify", envBool("S3_SKIP_TLS_VERIFY", false), "S3_SKIP_TLS_VERIFY - Skip TLS certificate verification")
}

// S3Collector struct
type S3Collector struct{}

// Describe - Implements prometheus.Collector
func (c S3Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- up
}

// Collect - Implements prometheus.Collector.
func (c S3Collector) Collect(ch chan<- prometheus.Metric) {
	metricsMutex.RLock()
	metrics := cachedMetrics
	err := cachedError
	metricsMutex.RUnlock()

	s3Status := 0
	if metrics.S3Status {
		s3Status = 1
	}

	if err != nil {
		ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(s3Status), s3Endpoint, s3Region)
		log.Errorf("Cached error: %v", err)
		return
	}

	ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(s3Status), s3Endpoint, s3Region)
	log.Debugf("Cached S3 metrics %s: %+v", s3Endpoint, metrics)

	descS := prometheus.NewDesc("s3_total_size", "S3 Total Bucket Size", []string{"s3Endpoint", "s3Region", "storageClass"}, nil)
	descON := prometheus.NewDesc("s3_total_object_number", "S3 Total Object Number", []string{"s3Endpoint", "s3Region", "storageClass"}, nil)
	descDS := prometheus.NewDesc("s3_list_total_duration_seconds", "Total time spent listing objects across all buckets", []string{"s3Endpoint", "s3Region"}, nil)

	for class, s3Metrics := range metrics.StorageClasses {
		ch <- prometheus.MustNewConstMetric(descS, prometheus.GaugeValue, s3Metrics.Size, s3Endpoint, s3Region, class)
		ch <- prometheus.MustNewConstMetric(descON, prometheus.GaugeValue, s3Metrics.ObjectNumber, s3Endpoint, s3Region, class)
	}
	ch <- prometheus.MustNewConstMetric(descDS, prometheus.GaugeValue, float64(metrics.TotalListDuration.Seconds()), s3Endpoint, s3Region)

	descBucketS := prometheus.NewDesc("s3_bucket_size", "S3 Bucket Size", []string{"s3Endpoint", "s3Region", "bucketName", "storageClass"}, nil)
	descBucketON := prometheus.NewDesc("s3_bucket_object_number", "S3 Bucket Object Number", []string{"s3Endpoint", "s3Region", "bucketName", "storageClass"}, nil)

	for _, bucket := range metrics.S3Buckets {
		for class, s3Metrics := range bucket.StorageClasses {
			ch <- prometheus.MustNewConstMetric(descBucketS, prometheus.GaugeValue, s3Metrics.Size, s3Endpoint, s3Region, bucket.BucketName, class)
			ch <- prometheus.MustNewConstMetric(descBucketON, prometheus.GaugeValue, s3Metrics.ObjectNumber, s3Endpoint, s3Region, bucket.BucketName, class)
		}
		descBucketDS := prometheus.NewDesc("s3_list_duration_seconds", "Time spent listing objects in bucket", []string{"s3Endpoint", "s3Region", "bucketName"}, nil)
		ch <- prometheus.MustNewConstMetric(descBucketDS, prometheus.GaugeValue, float64(bucket.ListDuration.Seconds()), s3Endpoint, s3Region, bucket.BucketName)
	}
}

func updateMetrics(interval time.Duration) {
	for {
		authCfg := auth.AuthConfig{
			Region:        s3Region,
			Endpoint:      s3Endpoint,
			AccessKey:     s3AccessKey,
			SecretKey:     s3SecretKey,
			SkipTLSVerify: s3SkipTLSVerify,
		}

		auth.DetectAuthMethod(&authCfg)

		awsAuth := auth.NewAWSAuth(authCfg)
		awsCfg, err := awsAuth.GetConfig(context.Background())
		if err != nil {
			log.Errorf("Failed to configure authentication: %v", err)
			time.Sleep(interval)
			continue
		}

		s3Conn := controllers.S3Conn{
			Endpoint:       s3Endpoint,
			Region:         s3Region,
			ForcePathStyle: s3ForcePathStyle,
			AWSConfig:      &awsCfg,
		}

		metrics, err := controllers.S3UsageInfo(s3Conn, s3BucketNames)

		metricsMutex.Lock()
		if !reflect.DeepEqual(cachedMetrics, metrics) {
			cachedMetrics = metrics
		}
		cachedError = err
		metricsMutex.Unlock()

		if err != nil {
			log.Errorf("Failed to update S3 metrics: %v", err)
		} else {
			log.Debugf("Updated S3 metrics: %+v", metrics)
		}

		log.Debugf("Waiting for %v before updating metrics", interval)
		time.Sleep(interval)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		log.Errorf("Error writing health response: %v", err)
	}
}

func main() {
	initFlags()
	flag.Parse()

	if logFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	} else {
		log.SetFormatter(&log.TextFormatter{})
	}

	level, err := log.ParseLevel(logLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %s", logLevel)
	}
	log.SetLevel(level)

	interval, err := time.ParseDuration(scrapeInterval)
	if err != nil {
		log.Fatalf("Invalid scrape interval: %s", scrapeInterval)
	}

	go updateMetrics(interval)

	prometheus.MustRegister(S3Collector{})

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", healthHandler)

	srv := &http.Server{
		Addr:         listenPort,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Infof("Starting server on %s", listenPort)
	if s3BucketNames != "" {
		log.Infof("Monitoring buckets: %s in %s region", s3BucketNames, s3Region)
	} else {
		log.Infof("Monitoring all buckets in %s region", s3Region)
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
