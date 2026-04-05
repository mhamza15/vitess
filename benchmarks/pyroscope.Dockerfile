FROM alpine:3.22 AS downloader

ARG PROFILECLI_VERSION=1.19.0
ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl tar && \
    curl -fL "https://github.com/grafana/pyroscope/releases/download/v${PROFILECLI_VERSION}/profilecli_${PROFILECLI_VERSION}_linux_${TARGETARCH}.tar.gz" | tar -xz -C /tmp && \
    chmod +x /tmp/profilecli

FROM grafana/pyroscope:latest

COPY --from=downloader /tmp/profilecli /usr/local/bin/profilecli
