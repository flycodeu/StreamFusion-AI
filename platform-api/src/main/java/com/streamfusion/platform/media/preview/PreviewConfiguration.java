package com.streamfusion.platform.media.preview;

import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties({
    PreviewProperties.class,
    com.streamfusion.platform.media.preview.node.PreviewNodeProperties.class
})
public class PreviewConfiguration {}
