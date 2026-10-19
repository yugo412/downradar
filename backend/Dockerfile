# Production image for the API and the two one-off commands.
#
# The seed and the user command ride along in the same image, so a shell on the
# server reaches them with `docker compose run` instead of asking for a second
# image that would repeat the whole dependency download.
#
# services.yaml is deliberately not baked in: it is bind-mounted at runtime, so
# writing a profile does not mean building a release.

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so they keep their own layer and are only fetched again
# when go.mod or go.sum moves.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is off so the result is a static binary that runs on a bare alpine.
RUN CGO_ENABLED=0 GOOS=linux \
      go build -trimpath -ldflags="-s -w" -o /out/downradar-api  ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux \
      go build -trimpath -ldflags="-s -w" -o /out/downradar-seed ./cmd/seed \
 && CGO_ENABLED=0 GOOS=linux \
      go build -trimpath -ldflags="-s -w" -o /out/downradar-user ./cmd/user

FROM alpine:3.22

# ca-certificates for the vendor status pages it dials, tzdata so the timestamps
# it writes mean what they say.
RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/ /usr/local/bin/

# Nothing here needs to be root. The process owns nothing on the filesystem and
# reaches the network and the database the way the container is set up to.
USER 65534:65534

EXPOSE 8080

# CMD rather than ENTRYPOINT, so `docker compose run api downradar-seed ...` runs
# the seed command instead of appending it to the API's arguments.
CMD ["downradar-api"]
