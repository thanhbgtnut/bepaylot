# syntax=docker/dockerfile:1
#
# Two images from one Dockerfile:
#   --target api     static binary, no cgo (role=api, or role=all with webassembly rendering)
#   --target worker  server + pdfium-worker linked against libpdfium (role=worker,
#                    parser.render.mode=multi_threaded) — spec §5.7
#
# PDFium comes from the pinned prebuilt binaries of bblanchon/pdfium-binaries.

ARG PDFIUM_VERSION=chromium/7350

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/skills-sync ./cmd/skills-sync

# libpdfium + pkg-config file, for the cgo pdfium-worker.
FROM golang:1.26 AS pdfium
ARG PDFIUM_VERSION
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends pkg-config curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    case "$TARGETARCH" in amd64) arch=x64 ;; arm64) arch=arm64 ;; *) echo "unsupported arch $TARGETARCH"; exit 1 ;; esac; \
    mkdir -p /opt/pdfium; \
    curl -fsSL "https://github.com/bblanchon/pdfium-binaries/releases/download/${PDFIUM_VERSION}/pdfium-linux-${arch}.tgz" | tar -xz -C /opt/pdfium; \
    mkdir -p /usr/lib/pkgconfig; \
    printf 'prefix=/opt/pdfium\nlibdir=/opt/pdfium/lib\nincludedir=/opt/pdfium/include\n\nName: PDFium\nDescription: PDFium\nVersion: 1\nLibs: -L/opt/pdfium/lib -lpdfium\nCflags: -I/opt/pdfium/include\n' > /usr/lib/pkgconfig/pdfium.pc
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -tags pdfium_cgo -trimpath -ldflags="-s -w" -o /out/pdfium-worker ./cmd/pdfium-worker

FROM gcr.io/distroless/static-debian12:nonroot AS api
WORKDIR /app
COPY --from=build /out/ /app/
COPY configs /app/configs
COPY skills /app/skills
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/configs/config.yaml", "-role", "api"]

FROM gcr.io/distroless/cc-debian12:nonroot AS worker
WORKDIR /app
COPY --from=build /out/ /app/
COPY --from=pdfium /out/pdfium-worker /app/pdfium-worker
COPY --from=pdfium /opt/pdfium/lib/ /usr/lib/
COPY configs /app/configs
COPY skills /app/skills
ENV BEPAYLOT_RENDER_MODE=multi_threaded \
    BEPAYLOT_PDFIUM_WORKER=/app/pdfium-worker \
    GOMEMLIMIT=700MiB
USER nonroot
ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/configs/config.yaml", "-role", "worker"]
