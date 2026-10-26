# Baratie : image Docker construite depuis les sources.
#   docker build -t baratie .
# Étape 1 : compilation (Go seul, aucune dépendance externe).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/baratie .

# Étape 2 : l'image finale. Alpine (et non scratch) pour ffmpeg/ffprobe,
# qui servent aux captures d'écran et à la lecture des pistes.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata ffmpeg
COPY --from=build /out/baratie /baratie
# Réglages, jeton d'accès, règles du tracker, cache, historique : tout dans /config.
ENV BARATIE_LISTEN=0.0.0.0:8769 \
    BARATIE_CONFIG=/config \
    BARATIE_DOCKER=1 \
    HOME=/config \
    TZ=Europe/Paris
VOLUME /config
EXPOSE 8769
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/baratie", "health"]
ENTRYPOINT ["/baratie"]
