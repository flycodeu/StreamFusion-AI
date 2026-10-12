package com.streamfusion.platform.media.runtime.config;

import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(MediaRuntimeProperties.class)
public class MediaRuntimeConfiguration {}
