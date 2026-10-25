FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download github.com/lib/pq
COPY cmd ./cmd
COPY internal ./internal
RUN go test -timeout 60s ./... && CGO_ENABLED=0 go build -trimpath -o /ai-trader ./cmd/ai-trader

# T-Invest requires the Russian Trusted CA chain (official T-Bank links).
# Keep HTTPS verification enabled when downloading the public certificates.
FROM debian:bookworm-slim AS certificates
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl openssl \
    && rm -rf /var/lib/apt/lists/*
RUN curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 60 \
      https://help-static2.tbank-online.com/certs/linux_russian_trusted_root_ca_pem/russian_trusted_root_ca_pem.crt \
      -o /usr/local/share/ca-certificates/russian_trusted_root_ca.crt \
    && curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 60 \
      https://help-static2.tbank-online.com/certs/russian_trusted_sub_ca_pem/russian_trusted_sub_ca_2024_pem.crt \
      -o /usr/local/share/ca-certificates/russian_trusted_sub_ca_2024.crt \
    && curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 60 \
      https://help-static2.tbank-online.com/certs/russian_trusted_sub_ca_pem/russian_trusted_sub_ca_pem.crt \
      -o /usr/local/share/ca-certificates/russian_trusted_sub_ca.crt \
    && for cert in /usr/local/share/ca-certificates/russian_trusted_*.crt; do \
         openssl x509 -in "$cert" -noout -subject -dates || exit 1; \
       done \
    && update-ca-certificates

# Export a native binary and CA bundle for the host's existing Codex login.
FROM scratch AS local-artifact
COPY --from=build /ai-trader /ai-trader
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /ca-certificates.crt

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=certificates /usr/local/share/ca-certificates/ /usr/local/share/ca-certificates/
RUN update-ca-certificates
COPY --from=build /ai-trader /usr/local/bin/ai-trader
USER 65532:65532
ENTRYPOINT ["ai-trader"]
CMD ["help"]
