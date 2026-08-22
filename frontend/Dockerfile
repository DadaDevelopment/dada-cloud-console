# Runtime-only image — build already ran in CI (see the "Frontend build"
# stage in Jenkinsfile, node-builder container, in parallel with the Go
# backend lane). NEXT_PUBLIC_* vars are baked into the JS bundle at that
# `npm run build` step, not here — this stage only packages the output.
FROM node:20-alpine
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
RUN addgroup --system --gid 1001 nodejs \
 && adduser --system --uid 1001 nextjs
COPY public ./public
COPY --chown=nextjs:nodejs .next/standalone ./
COPY --chown=nextjs:nodejs .next/static ./.next/static
USER nextjs
EXPOSE 3000
ENV PORT=3000
CMD ["node", "server.js"]
