# syntax=docker/dockerfile:1

# The web app's image: Caddy serving the built SPA and proxying /api to the API.
# The build context is this directory.

# The same Node major as development, because npm lockfile formats change between majors.
FROM node:24-alpine AS builder

WORKDIR /web

# Dependencies first, in a layer of their own, so a source change reuses it.
COPY package.json package-lock.json .npmrc ./
RUN npm ci --no-audit --no-fund

COPY . .
RUN npm run build


FROM caddy:2.11-alpine

LABEL org.opencontainers.image.source="https://github.com/datapointchris/projects" \
      org.opencontainers.image.description="The projects web app: the SPA, served by Caddy beside a proxy to the projects API" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /web/dist /srv
COPY Caddyfile.docker /etc/caddy/Caddyfile

# Caddy writes its instance id under /data and reads /config. A cluster mounts
# both writable over a read-only root, and anywhere else the image's own copies
# have to be writable by the process.
RUN chown 10501:10501 /data /config

# A numeric uid, so a cluster's runAsNonRoot can check it.
USER 10501:10501
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["wget", "-qO-", "http://localhost:8080/health"]

CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
