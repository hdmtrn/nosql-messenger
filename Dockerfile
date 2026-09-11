# Stage 1: frontend. web/dist is in .gitignore, so there is nowhere to take it
# from except building it here.
FROM node:22-alpine AS web

WORKDIR /web

# The manifests are copied separately from the sources: the npm ci layer is
# reused as long as the dependencies have not changed. Editing a .vue file does
# not invalidate it.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build


# Stage 2: backend.
FROM golang:1.26-alpine AS build

WORKDIR /src

# Same trick: modules are downloaded once and survive edits to *.go.
COPY go.mod go.sum ./
RUN go mod download

COPY server/ ./server/

# CGO_ENABLED=0 gives a static binary: the final image is alpine with musl,
# while the Go toolchain links against glibc, so a dynamic binary would not
# start there. As a side effect Go switches to its own DNS resolver instead of
# the system getaddrinfo.
# -trimpath strips absolute build paths from the binary; -s -w drop the symbol
# table and DWARF (~30% of the size). Panic stack traces stay readable: Go keeps
# function names separately from DWARF, in pclntab.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/messenger ./server


# Stage 3: what ships to the instance. Neither node nor the Go toolchain is here.
FROM alpine:3.22

# autocert needs the root certificates: the ACME exchange with Let's Encrypt
# goes over HTTPS.
RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 10001 app

WORKDIR /app

# server.go looks for static files at the RELATIVE path web/dist, so the layout
# inside the image must mirror the repository layout relative to WORKDIR.
COPY --from=build /out/messenger ./messenger
COPY --from=web   /web/dist      ./web/dist

# The process runs as an unprivileged user, hence 8080 rather than 80: ports
# below 1024 need root or CAP_NET_BIND_SERVICE. Publishing 80 and 443 outside
# is docker's job, i.e. compose's, not the image's.
USER app
EXPOSE 8080

ENTRYPOINT ["/app/messenger"]
