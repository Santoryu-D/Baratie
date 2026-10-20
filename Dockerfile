# Image de D3X en mode serveur (NAS, serveur). Elle reprend les binaires Linux
# compilés et signés par le workflow Release (dist/), sans rien recompiler.
#
#   docker run -d --name d3x -p 8733:8733 -v d3x:/config -v /mes/medias:/data:ro ghcr.io/v3x0rv3x/d3x
#   docker logs d3x   # le lien d'accès à la page
FROM --platform=$BUILDPLATFORM alpine:3.22 AS base
RUN apk add --no-cache ca-certificates tzdata \
 && mkdir -p /out/config /out/data \
 && mkdir -m 1777 /out/tmp

FROM scratch
ARG TARGETARCH
COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=base /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=base /out/tmp /tmp
COPY --from=base --chown=1000:1000 /out/config /config
COPY --from=base /out/data /data
COPY dist/d3x_linux_${TARGETARCH} /d3x
# Réglages, jeton d'accès, cache de hachage et clés SSH : tout dans /config.
ENV D3X_LISTEN=0.0.0.0:8733 \
    D3X_MANAGED=docker \
    HOME=/config \
    XDG_CONFIG_HOME=/config \
    TZ=Europe/Paris
VOLUME /config
EXPOSE 8733
USER 1000:1000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --start-interval=2s CMD ["/d3x", "health"]
ENTRYPOINT ["/d3x"]
