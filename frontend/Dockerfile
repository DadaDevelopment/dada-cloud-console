# Build stage — NEXT_PUBLIC_* vars are baked into the JS bundle here.
# Pass them as build args: docker build \
#   --build-arg NEXT_PUBLIC_AUTH_MODE=oidc \
#   --build-arg NEXT_PUBLIC_KEYCLOAK_ISSUER=https://id.dada-tuda.ru/realms/master \
#   --build-arg NEXT_PUBLIC_OIDC_CLIENT_ID=dada-console \
#   -f frontend/Dockerfile frontend/
FROM node:20-alpine AS builder
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1

ARG NEXT_PUBLIC_AUTH_MODE=local
ARG NEXT_PUBLIC_KEYCLOAK_ISSUER=
ARG NEXT_PUBLIC_OIDC_CLIENT_ID=dada-console
ARG NEXT_PUBLIC_API_URL=

ENV NEXT_PUBLIC_AUTH_MODE=$NEXT_PUBLIC_AUTH_MODE
ENV NEXT_PUBLIC_KEYCLOAK_ISSUER=$NEXT_PUBLIC_KEYCLOAK_ISSUER
ENV NEXT_PUBLIC_OIDC_CLIENT_ID=$NEXT_PUBLIC_OIDC_CLIENT_ID
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL

# @dada/* deps come from internal Nexus npm (see .npmrc). Auth is provided as a
# BuildKit secret so it never lands in an image layer:
#   DOCKER_BUILDKIT=1 docker build --secret id=nexus_npm_auth,src=<file-with-base64-user:pass> ...
COPY package*.json .npmrc ./
RUN --mount=type=secret,id=nexus_npm_auth \
    NEXUS_NPM_AUTH="$(cat /run/secrets/nexus_npm_auth 2>/dev/null)" npm ci --prefer-offline
COPY . .
RUN npm run build

# Runtime stage — only the standalone output.
FROM node:20-alpine
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
RUN addgroup --system --gid 1001 nodejs \
 && adduser --system --uid 1001 nextjs
COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
USER nextjs
EXPOSE 3000
ENV PORT=3000
CMD ["node", "server.js"]
