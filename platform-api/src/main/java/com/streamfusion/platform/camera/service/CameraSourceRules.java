package com.streamfusion.platform.camera.service;

import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.pojo.dto.SecretWrite;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.net.InetAddress;
import java.util.Locale;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Configuration validation only. DNS resolution and RTSP requests are not performed on save. */
@Component
@RequiredArgsConstructor
public class CameraSourceRules {
    private final CameraProperties properties;

    public static String text(String value, int max, boolean required) {
        if (value == null) {
            if (required) throw invalid();
            return null;
        }
        value = value.strip();
        if ((required && value.isEmpty())
                || value.codePointCount(0, value.length()) > max
                || value.codePoints().anyMatch(Character::isISOControl)) throw invalid();
        return value;
    }

    public static String secret(SecretWrite input, String previous, int max, boolean allowClear) {
        if (input == null) {
            if (previous == null) throw invalid();
            return previous;
        }
        if (input.action() == null) throw invalid();
        return switch (input.action()) {
            case "KEEP" -> {
                if (input.value() != null || previous == null) throw invalid();
                yield previous;
            }
            case "CLEAR" -> {
                if (!allowClear || input.value() != null) throw invalid();
                yield "";
            }
            case "REPLACE" -> {
                String value = input.value();
                if (value == null
                        || (!allowClear && value.isEmpty())
                        || value.codePointCount(0, value.length()) > max
                        || value.codePoints().anyMatch(Character::isISOControl)) throw invalid();
                yield value;
            }
            default -> throw invalid();
        };
    }

    public void policy(String key) {
        if (key == null || !properties.getNetworkPolicies().containsKey(key)) throw invalid();
    }

    public String host(String value, String policyKey) {
        policy(policyKey);
        value = storedHost(value);
        var policy = properties.getNetworkPolicies().get(policyKey);
        byte[] literal = literal(value);
        if (literal != null) {
            try {
                var address = InetAddress.getByAddress(literal);
                if (address.isAnyLocalAddress()
                        || address.isLoopbackAddress()
                        || address.isLinkLocalAddress()
                        || address.isMulticastAddress()) throw invalid();
                if (policy.getCidrs().stream().noneMatch(cidr -> contains(cidr, literal)))
                    throw invalid();
                return value;
            } catch (java.net.UnknownHostException ex) {
                throw invalid();
            }
        }
        if (value.equals("localhost")
                || value.endsWith(".localhost")
                || policy.getHosts().stream().noneMatch(value::equalsIgnoreCase)) throw invalid();
        return value;
    }

    /**
     * Syntax-only storage validation. This never resolves a hostname or authorizes an external
     * call.
     */
    public String storedHost(String value) {
        if (value == null
                || value.length() > 253
                || value.isBlank()
                || !value.equals(value.strip())
                || value.indexOf('%') >= 0) throw invalid();
        byte[] literal = literal(value);
        if (literal != null) {
            try {
                return InetAddress.getByAddress(literal).getHostAddress();
            } catch (java.net.UnknownHostException ex) {
                throw invalid();
            }
        }
        String name = value.toLowerCase(Locale.ROOT);
        if (!name.matches(
                "(?=.{1,253}$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*"))
            throw invalid();
        return name;
    }

    private static byte[] literal(String value) {
        try {
            if (value.matches("[0-9]+(?:\\.[0-9]+){3}")) {
                String[] parts = value.split("\\.");
                byte[] bytes = new byte[4];
                for (int i = 0; i < 4; i++) {
                    if (parts[i].length() > 3
                            || (parts[i].length() > 1 && parts[i].startsWith("0"))) throw invalid();
                    int n = Integer.parseInt(parts[i]);
                    if (n > 255) throw invalid();
                    bytes[i] = (byte) n;
                }
                return bytes;
            }
            if (value.contains(":") && value.matches("[0-9A-Fa-f:]+"))
                return InetAddress.getByName(value).getAddress();
            return null;
        } catch (java.net.UnknownHostException | NumberFormatException ex) {
            throw invalid();
        }
    }

    private static boolean contains(String cidr, byte[] value) {
        try {
            String[] parts = cidr.split("/", -1);
            if (parts.length != 2) return false;
            byte[] network = literal(parts[0]);
            int bits = Integer.parseInt(parts[1]);
            if (network == null
                    || network.length != value.length
                    || bits < 0
                    || bits > network.length * 8) return false;
            for (int i = 0; i < network.length; i++) {
                int used = Math.min(8, Math.max(0, bits - i * 8));
                int mask = (0xff << (8 - used)) & 0xff;
                if ((network[i] & mask) != (value[i] & mask)) return false;
            }
            return true;
        } catch (RuntimeException ex) {
            return false;
        }
    }

    public static void port(Integer port) {
        if (port == null || port < 1 || port > 65535) throw invalid();
    }

    public static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }
}
