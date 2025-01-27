# S3bucket Exporter

s3-bucket-exporter collects informations about size and object list about all the buckets accessible by user. 
Was designed to work with AWS, but should work will all S3 compatible endpoints (Minio, Ceph, Localstack, etc).

## Getting started

Run from command-line:

```sh
./s3-bucket-exporter [flags]
```

Run from command-line - example with minimal parameter list:

```sh
./s3-bucket-exporter -s3_endpoint=http://127.0.0.1:9000 -s3_access_key=minioadmin -s3_secret_key=minioadmin
```

Run as docker container - example for local s3-like buckets:

```sh
docker run -p 9655:9655 -d -e S3_ENDPOINT=http://127.0.0.1:9000 -e S3_ACCESS_KEY=minioadmin -e S3_SECRET_KEY=minioadmin -e S3_BUCKET_NAMES=my-bucket-name docker.io/tropnikovvl/s3-bucket-exporter:latest
```

Run from command-line - example for AWS
/*Please note that you need to have buckets only in one region. Otherwise script will fail with message "...BucketRegionError: incorrect region, the bucket is not in ..."*/

```sh
./s3-bucket-exporter -s3_access_key ABCD12345678 -s3_secret_key mySecretKey -s3_bucket_names=my-bucket-name -s3_region=us-east-1
```

The exporter supports two different configuration ways: command-line arguments take precedence over environment variables.

As for available flags and equivalent environment variables, here is a list:

|     environment variable          |    argument                      |     description                                    | default |     example              |
| --------------------------------- | -------------------------------- | -------------------------------------------------- |---------| ------------------------ |
| S3_BUCKET_NAMES                   | -s3_bucket_names                 | If used, then only it is scraped, if not, then all buckets in the region            |         | my-bucket-name,my-another-bucket            |
| S3_ENDPOINT                       | -s3_endpoint                     | S3 endpoint url with port                          | s3.us-east-1.amazonaws.com | http://127.0.0.1:9000         |
| S3_ACCESS_KEY                     | -s3_access_key                   | S3 access_key (aws_access_key)                     |         | minioadmin               |
| S3_SECRET_KEY                     | -s3_secret_key                   | S3 secret key (aws_secret_key)                     |         | minioadmin              |
| S3_REGION                         | -s3_region                       | S3 region name                                     | us-east-1 | eu-west-1 |
| S3_FORCE_PATH_STYLE               | -s3_force_path_style             | Force use path style (bucketname not added to url) | False   | True                    |
| LISTEN_PORT                       | -listen_port                     | Exporter listen Port cluster                       | :9655   | :9123                   |
| LOG_LEVEL                         | -log_level                       | Log level. Info or Debug                           | Info    | Debug                   |
| LOG_FORMAT                        | -log_format                      | Log format. Json or text                           | text    | json                    |
| SCRAPE_INTERVAL                   | -scrape_interval                 | Scrape interval                                    | 5m      | 30s                     |
| USE_IAM_ROLE                      | -use_iam_role                    | Use IAM role instead of access keys                | false   | true                    |

> Warning: For security reason is not advised to use credential from command line

## Prometheus configuration example:

```yaml
  - job_name: 's3bucket'
    static_configs:
    - targets: ['192.168.0.5:9655']
```

## Grafana

Grafana dashboad ([resources/grafana-s3bucket-dashboard.json] (resources/grafana-s3bucket-dashboard.json)):

![](images/grafana-s3bucket-dashboard.png)
