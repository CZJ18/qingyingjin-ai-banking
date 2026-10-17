ARG NODE_BUILD_IMAGE=node:22-alpine
ARG NGINX_RUNTIME_IMAGE=nginx:1.28-alpine
FROM ${NODE_BUILD_IMAGE} AS build
WORKDIR /app
COPY frontend/ai-banking/package.json frontend/ai-banking/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ai-banking/ ./
RUN npm run build

FROM ${NGINX_RUNTIME_IMAGE} AS runtime
COPY deploy/banking-web/nginx.conf /etc/nginx/nginx.conf
COPY --from=build --chown=101:101 /app/dist/ /usr/share/nginx/html/
USER 101:101
EXPOSE 8080
ENTRYPOINT ["nginx", "-g", "daemon off;"]
