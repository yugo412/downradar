# Production image for the Nuxt server.
#
# The build must not see a secret: Nuxt inlines whatever nuxt.config.ts reads,
# and ci.yml refuses a config that reads one. The site address, the internal
# token and the proxy target are therefore handed over at runtime as NUXT_*
# variables, which Nitro picks up when the server starts.
#
# The one exception is the page cache, which decides route rules and is baked
# into the bundle, so it arrives as a build argument.

FROM node:24-alpine AS build

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .

ARG NUXT_PAGE_CACHE_SECONDS=60
ENV NUXT_PAGE_CACHE_SECONDS=${NUXT_PAGE_CACHE_SECONDS}

RUN npx nuxt build

FROM node:24-alpine

WORKDIR /app

ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000

# Nitro bundles what it needs, so the runtime carries no node_modules.
COPY --from=build /app/.output ./

USER 65534:65534

EXPOSE 3000

CMD ["node", "server/index.mjs"]
