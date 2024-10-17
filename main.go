package main

import (
	"flag"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/tropnikovvl/s3bucket_exporter/controllers"
)

var (
	up                          = prometheus.NewDesc("s3_endpoint_up", "Connection to S3 successful", []string{"s3Endpoint"}, nil)
	listenPort                  = ":9655"
	s3Endpoint                  = ""
	s3AccessKey                 = ""
	s3SecretKey                 = ""
	s3DisableSSL                = false
	s3BucketName                = ""
	s3DisableEndpointHostPrefix = false
	s3ForcePathStyle            = false
	s3Region                    = "us-east-1"
	s3Conn                      controllers.S3Conn
	logLevel                    = "info"
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

func init() {
	flag.StringVar(&s3Endpoint, "s3_endpoint", envString("S3_ENDPOINT", s3Endpoint), "S3_ENDPOINT - eg. myceph.com:7480")
	flag.StringVar(&s3AccessKey, "s3_access_key", envString("S3_ACCESS_KEY", s3AccessKey), "S3_ACCESS_KEY - aws_access_key")
	flag.StringVar(&s3SecretKey, "s3_secret_key", envString("S3_SECRET_KEY", s3SecretKey), "S3_SECRET_KEY - aws_secret_key")
	flag.StringVar(&s3BucketName, "s3_bucket_name", envString("S3_BUCKET_NAME", s3BucketName), "S3_BUCKET_NAME")
	flag.StringVar(&s3Region, "s3_region", envString("S3_REGION", s3Region), "S3_REGION")
	flag.StringVar(&listenPort, "listen_port", envString("LISTEN_PORT", listenPort), "LISTEN_PORT e.g ':9655'")
	flag.StringVar(&logLevel, "log_level", envString("LOG_LEVEL", logLevel), "LOG_LEVEL")
	flag.BoolVar(&s3DisableSSL, "s3_disable_ssl", envBool("S3_DISABLE_SSL", s3DisableSSL), "s3 disable ssl")
	flag.BoolVar(&s3DisableEndpointHostPrefix, "s3_disable_endpoint_host_prefix", envBool("S3_DISABLE_ENDPOINT_HOST_PREFIX", s3DisableEndpointHostPrefix), "S3_DISABLE_ENDPOINT_HOST_PREFIX")
	flag.BoolVar(&s3ForcePathStyle, "s3_force_path_style", envBool("S3_FORCE_PATH_STYLE", s3ForcePathStyle), "S3_FORCE_PATH_STYLE")
	flag.Parse()
}

// S3Collector dummy struct
type S3Collector struct {
}

// Describe - Implements prometheus.Collector
func (c S3Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- up
}

// Collect - Implements prometheus.Collector.
func (c S3Collector) Collect(ch chan<- prometheus.Metric) {
	s3Conn = controllers.S3Conn{
		S3ConnEndpoint:                  s3Endpoint,
		S3ConnAccessKey:                 s3AccessKey,
		S3ConnSecretKey:                 s3SecretKey,
		S3ConnDisableSsl:                s3DisableSSL,
		S3ConnDisableEndpointHostPrefix: s3DisableEndpointHostPrefix,
		S3ConnForcePathStyle:            s3ForcePathStyle,
		S3ConnRegion:                    s3Region,
	}

	s3metrics, err := controllers.S3UsageInfo(s3Conn, s3BucketName)

	s3Status := 0
	if s3metrics.S3Status {
		s3Status = 1
	}

	if err != nil {
		ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(s3Status), s3Endpoint)
		log.Errorf("Failed to fetch S3 metrics: %v", err)
		return
	}

	ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(s3Status), s3Endpoint)
	log.Debug("s3metrics read from s3_endpoint :", s3metrics)

	descS := prometheus.NewDesc("s3_total_size", "S3 Total Bucket Size", []string{"s3Endpoint"}, nil)
	descON := prometheus.NewDesc("s3_total_object_number", "S3 Total Object Number", []string{"s3Endpoint"}, nil)
	ch <- prometheus.MustNewConstMetric(descS, prometheus.GaugeValue, float64(s3metrics.S3Size), s3Endpoint)
	ch <- prometheus.MustNewConstMetric(descON, prometheus.GaugeValue, float64(s3metrics.S3ObjectNumber), s3Endpoint)

	for _, bucket := range s3metrics.S3Buckets {
		descBucketS := prometheus.NewDesc("s3_bucket_size", "S3 Bucket Size", []string{"s3Endpoint", "bucketName"}, nil)
		descBucketON := prometheus.NewDesc("s3_bucket_object_number", "S3 Bucket Object Number", []string{"s3Endpoint", "bucketName"}, nil)

		ch <- prometheus.MustNewConstMetric(descBucketS, prometheus.GaugeValue, float64(bucket.BucketSize), s3Endpoint, bucket.BucketName)
		ch <- prometheus.MustNewConstMetric(descBucketON, prometheus.GaugeValue, float64(bucket.BucketObjectNumber), s3Endpoint, bucket.BucketName)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	_, err := w.Write([]byte("OK"))
	if err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		log.Fatalf("Error writing response: %v", err)
	}
}

func main() {
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %v", logLevel)
	}
	log.SetLevel(level)

	if s3AccessKey == "" || s3SecretKey == "" {
		log.Fatal("Missing required S3 configuration")
	}

	c := S3Collector{}
	prometheus.MustRegister(c)

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", healthHandler)

	log.Infof("Beginning to serve on port %s", listenPort)
	if s3BucketName != "" {
		log.Infof("Monitoring S3 bucket: %s", s3BucketName)
	} else {
		log.Infof("Monitoring all S3 buckets in the %s region", s3Region)
	}

	srv := &http.Server{
		Addr:         listenPort,
		Handler:      nil,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
