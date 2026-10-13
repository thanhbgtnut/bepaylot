# Stage 1: build the web app and the binaries.
# The base image (cicd/tanzu-dev/Dockerfile.base) already holds Go, Node, libpdfium, the Go
# module cache and the frontend node_modules, so this build needs no apt / npm access.
ARG BASE_IMAGE=k8sdev.mbbank.com.vn/cicd-common/bepaylot-base:1.0.0
ARG RUNTIME_IMAGE=k8sdev.mbbank.com.vn/cicd-common/debian:bookworm-slim

FROM ${BASE_IMAGE} AS builder

ARG GIT_TOKEN
LABEL stage=gobuilder

ENV GOPROXY http://10.1.12.177:8445/repository/repo-go-proxy/,direct
ENV GOSUMDB off

COPY . /app

# Web app: Vite writes the build into internal/webui/dist, embedded by the server binary.
WORKDIR /app/frontend
RUN cmp -s package-lock.json /opt/frontend/package-lock.json \
    || { echo "frontend/package-lock.json differs from the base image: rebuild cicd/tanzu-dev/Dockerfile.base"; exit 1; }
RUN cp -a /opt/frontend/node_modules ./node_modules && npm run build

WORKDIR /app

# Modules are already in the base image; only ones added since then come from Nexus.
RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/skills-sync ./cmd/skills-sync \
 && CGO_ENABLED=1 GOOS=linux go build -tags pdfium_cgo -trimpath -ldflags="-s -w" -o /out/pdfium-worker ./cmd/pdfium-worker

# Stage 2: runtime
FROM ${RUNTIME_IMAGE}

WORKDIR /app

COPY --from=builder /out/ /app/
COPY --from=builder /app/configs /app/configs
COPY --from=builder /app/skills /app/skills
# Runtime files the slim image lacks, taken from the base image instead of apt.
COPY --from=builder /opt/pdfium/lib/libpdfium.so /usr/lib/libpdfium.so
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV BEPAYLOT_RENDER_MODE=multi_threaded \
    BEPAYLOT_PDFIUM_WORKER=/app/pdfium-worker

RUN mkdir -p /logs /app/data && touch /logs/app.log

# Create a group and user
RUN addgroup --gid 10000 appadmin && \
    adduser --uid 10000 --ingroup appadmin --disabled-password appadmin && \
    chown -R appadmin:appadmin /app /logs

# Set the default user.
USER appadmin

EXPOSE 8080

VOLUME /app /tmp /logs /home/appadmin

ENTRYPOINT ["/app/server"]
