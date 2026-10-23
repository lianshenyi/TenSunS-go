ARG NODE_IMAGE=node:14-alpine
ARG NGINX_IMAGE=nginx:1.24-alpine

FROM ${NODE_IMAGE} AS builder
WORKDIR /vue
COPY package.json ./
RUN npm install --registry=https://registry.npmmirror.com --no-audit --no-fund
COPY . .
RUN npm run build:prod

FROM ${NGINX_IMAGE}
WORKDIR /www
COPY --from=builder /vue/http-ops.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /vue/dist dist/
