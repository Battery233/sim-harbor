# Static assets are shared by all targets; run Node/WASM on the native builder.
FROM --platform=$BUILDPLATFORM node:26.7.0-alpine3.23 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm test && npm run build

FROM golang:1.26.9-alpine3.23 AS backend
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY configs/ ./configs/
RUN go test -count=1 ./... && go vet ./...
RUN mkdir -m 0700 -p /out/rootfs/data && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sim ./cmd/sim && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sim-check ./cmd/check
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sim-data ./cmd/maintain

FROM scratch
LABEL org.opencontainers.image.title="SIMHarbor" \
      org.opencontainers.image.version="1.1.8" \
      org.opencontainers.image.source="https://github.com/Battery233/sim-harbor"
WORKDIR /app
ENV TMPDIR=/data
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=backend --chown=65532:65532 /out/rootfs/ /
COPY --from=backend /out/sim /out/sim-check /out/sim-data ./
COPY --from=web /src/web/dist ./web/dist
USER 65532:65532
EXPOSE 8080 9000
ENTRYPOINT ["/app/sim"]
CMD ["-config", "/config/runtime.json"]
