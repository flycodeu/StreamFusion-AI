package com.streamfusion.platform.common.security;

import org.springframework.context.annotation.Configuration;
import org.springframework.web.servlet.config.annotation.InterceptorRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;

/** 为 MVC 控制器注册统一的模块鉴权边界。 */
@Configuration(proxyBeanMethods = false)
public class ModuleAuthorizationConfiguration implements WebMvcConfigurer {
    private final ModuleAuthorizationInterceptor interceptor;

    public ModuleAuthorizationConfiguration(ModuleAuthorizationInterceptor interceptor) {
        this.interceptor = interceptor;
    }

    @Override
    public void addInterceptors(InterceptorRegistry registry) {
        registry.addInterceptor(interceptor);
    }
}
