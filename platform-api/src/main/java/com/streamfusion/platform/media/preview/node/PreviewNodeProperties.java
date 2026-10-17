package com.streamfusion.platform.media.preview.node;

import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "platform.preview-node", ignoreUnknownFields = false)
public record PreviewNodeProperties(
        @DefaultValue("") String connectionFile, @DefaultValue("") String credentialDirectory) {}
