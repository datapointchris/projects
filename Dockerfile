# syntax=docker/dockerfile:1

# The API's image. The TUI installs with `go install` and never reads this.
FROM golang:1.26.11-alpine AS builder

WORKDIR /src

# Dependencies first, in a layer of their own, so a source change reuses it.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The release names the version it builds. A build that names none reads as dev.
ARG PROJECTS_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${PROJECTS_VERSION}" -o /projects-api ./api


FROM alpine:3.22

LABEL org.opencontainers.image.source="https://github.com/datapointchris/projects" \
      org.opencontainers.image.description="The projects API: projects, items, dependencies and tasks over HTTP on Postgres" \
      org.opencontainers.image.licenses="MIT"

# The identity provider is reached over TLS, and log timestamps name a zone.
RUN apk add --no-cache ca-certificates tzdata

# Owned by root, so the process cannot rewrite its own code, and the root filesystem can mount read-only.
COPY --from=builder /projects-api /usr/local/bin/projects-api

# A numeric uid, so a cluster's runAsNonRoot can check it.
USER 10500:10500
EXPOSE 8000

# Migrations run as their own step before a rollout: `projects-api migrate` in this image.
CMD ["projects-api"]
