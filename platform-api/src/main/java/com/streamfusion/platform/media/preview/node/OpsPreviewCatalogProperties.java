package com.streamfusion.platform.media.preview.node;

import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

/** Optional, server-side connection to the Ops preview-instance catalog. */
@ConfigurationProperties(prefix = "platform.preview-ops", ignoreUnknownFields = false)
public record OpsPreviewCatalogProperties(
        @DefaultValue("") String baseUri, @DefaultValue("") String tokenFile) {}
