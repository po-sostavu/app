FROM node:24.19.0-alpine AS dependencies
WORKDIR /app/apps/pwa
RUN npm install --global npm@11.6.2
COPY apps/pwa/package.json apps/pwa/package-lock.json apps/pwa/.npmrc ./
RUN npm ci --no-fund

FROM dependencies AS build
COPY .prettierrc.json .prettierignore /app/
COPY apps/pwa/ ./
ARG VITE_API_BASE_URL=/api
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

FROM caddy:2.11.9-alpine AS runtime
RUN apk add --no-cache 'zlib>=1.4-r0' \
    && addgroup -S -g 10001 pwa \
    && adduser -S -u 10001 -G pwa pwa \
    && chown -R pwa:pwa /data /config
COPY apps/pwa/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/apps/pwa/dist /srv
USER pwa
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
