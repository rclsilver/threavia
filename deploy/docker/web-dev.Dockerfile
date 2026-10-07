# The web client in development.
#
# The sources are mounted, not copied: Vite watches them and the browser
# reloads, so changing a component never rebuilds or restarts the Go binary.
# Only the dependencies are baked in, which is what makes a container start in
# seconds instead of reinstalling node_modules on every run.
FROM node:22-alpine

WORKDIR /app

# Installed against the lockfile at build time. A changed dependency rebuilds
# this layer; a changed component does not.
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci --no-audit --no-fund

# node_modules lives in the image, so the bind mount over /app must not hide it.
# Vite is told to look here, and the compose file mounts an empty volume over
# /app/node_modules to keep the host out of the way.
ENV PATH=/app/node_modules/.bin:$PATH

EXPOSE 5173
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]
