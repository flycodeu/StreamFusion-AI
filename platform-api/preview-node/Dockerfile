# Resolve both upstream images to immutable digests in build-package.sh.
ARG JRE_IMAGE
ARG ZLM_IMAGE
FROM ${JRE_IMAGE} AS java
FROM ${ZLM_IMAGE}
USER root
COPY --from=java /opt/java/openjdk /opt/java/openjdk
ENV JAVA_HOME=/opt/java/openjdk
ENV PATH=/opt/java/openjdk/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# Namespace-local network enforcement is part of the preview appliance, not a platform service.
RUN apt-get update && apt-get install -y --no-install-recommends iptables util-linux && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /opt/preview/empty /state && chmod 755 /opt/preview /state && ln -s /opt/media/bin/MediaServer /opt/media/MediaServer && cp /opt/media/conf/config.ini /opt/media/config.ini
COPY preview-node.jar /opt/preview/preview-node.jar
COPY entrypoint.sh /opt/preview/entrypoint.sh
COPY logback-node.xml /opt/preview/logback-node.xml
RUN chmod 755 /opt/preview/entrypoint.sh && test -x /opt/media/MediaServer && command -v ffmpeg && java -version
WORKDIR /opt/preview
LABEL com.streamfusion.preview.maintenance="1"
ENTRYPOINT ["/opt/preview/entrypoint.sh"]
