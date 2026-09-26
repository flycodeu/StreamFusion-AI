package com.streamfusion.platform.common.security;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Declares the menu/function module that protects a controller endpoint.
 *
 * <p>Roles are assigned to menus/modules. Individual endpoint permission codes are not an
 * administrator-facing configuration item.
 */
@Target({ElementType.TYPE, ElementType.METHOD})
@Retention(RetentionPolicy.RUNTIME)
public @interface ModuleAccess {
    String value();
}
