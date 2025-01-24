FROM golang:1.23 AS builder

WORKDIR /build

COPY . .

RUN CGO_ENABLED=0 go build -a

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /build/s3bucket_exporter /

ENTRYPOINT ["/s3bucket_exporter"]
