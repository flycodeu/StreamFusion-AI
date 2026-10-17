ARG ZLM_IMAGE
FROM ${ZLM_IMAGE}
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/* \
    && ln -s /opt/media/bin/MediaServer /opt/media/MediaServer \
    && mkdir -p /state/logs \
    && chown -R 10001:10001 /state \
    && test -x /opt/media/MediaServer \
    && command -v ffmpeg \
    && command -v curl
WORKDIR /opt/media
LABEL com.streamfusion.zlm.direct="1"
