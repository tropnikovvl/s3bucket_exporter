FROM golang:1.23 AS builder

WORKDIR /build

COPY ./ /build

RUN CGO_ENABLED=0 go build -a

FROM busybox

COPY --from=builder /build/s3bucket_exporter /bin/s3bucket_exporter

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

WORKDIR /tmp

ENTRYPOINT ["/bin/s3bucket_exporter"]
