package com.streamfusion.platform.auth.guard;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.stereotype.Component;

@Component
public class ClientIpResolver {
    public static final String ATTRIBUTE = "com.streamfusion.clientIp";
    private final IpGuardProperties properties;

    public ClientIpResolver(IpGuardProperties properties) {
        this.properties = properties;
    }

    public String resolve(HttpServletRequest request) {
        Object saved = request.getAttribute(ATTRIBUTE);
        if (saved instanceof String ip) return ip;
        try {
            String current = IpAddresses.canonical(request.getRemoteAddr());
            if (properties.trustedProxies().contains(current)) {
                String forwarded = request.getHeader("X-Forwarded-For");
                if (forwarded != null) {
                    if (java.util.Collections.list(request.getHeaders("X-Forwarded-For")).size()
                            != 1) throw new IllegalArgumentException();
                    if (forwarded.length() > 1024) throw new IllegalArgumentException();
                    String[] chain = forwarded.split(",", -1);
                    if (chain.length > 16) throw new IllegalArgumentException();
                    for (int i = chain.length - 1;
                            i >= 0 && properties.trustedProxies().contains(current);
                            i--) current = IpAddresses.canonical(chain[i].strip());
                }
            }
            request.setAttribute(ATTRIBUTE, current);
            return current;
        } catch (IllegalArgumentException ex) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
    }
}
