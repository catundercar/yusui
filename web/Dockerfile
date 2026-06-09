# Build the Vue SPA, then serve it with nginx which also proxies /api (REST +
# WebSocket) to the server, so the browser talks to a single origin.
FROM node:20 AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
