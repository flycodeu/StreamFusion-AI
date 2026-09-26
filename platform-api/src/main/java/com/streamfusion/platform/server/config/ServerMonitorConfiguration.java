package com.streamfusion.platform.server.config;

import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(ServerMonitorProperties.class)
public class ServerMonitorConfiguration {}
