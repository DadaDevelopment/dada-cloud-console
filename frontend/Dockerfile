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

COPY package*.json ./
RUN npm ci --prefer-offline
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
