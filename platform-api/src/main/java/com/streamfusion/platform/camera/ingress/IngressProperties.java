package com.streamfusion.platform.camera.ingress;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;
import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;

/** Deployment-owned listeners and allowlists. No listeners are opened by default. */
@Getter
@Setter
@Component
@ConfigurationProperties(prefix = "platform.camera.ingress")
public class IngressProperties {
    private boolean enabled;
    private String mediaHost = "";
    private int rtspPort = 554;
    private int hookPort = 18091;
    private String hookToken = "";
    private Map<String, String> publishTokens = new LinkedHashMap<>();
    private boolean gbEnabled;
    private String sipBindHost = "127.0.0.1";
    private String sipAdvertisedHost = "";
    private int sipPort = 15060;
    private String serverId = "34020000002000000001";
    private String domain = "3402000000";
    private int rtpPortStart = 30000;
    private int maxSessions = 32;
    private int rtpTcpMode;
    private Map<String, Device> devices = new LinkedHashMap<>();

    @Getter
    @Setter
    public static class Device {
        private String password = "";
        private Set<String> remoteIps = Set.of();
        private Set<String> channels = Set.of();
    }

    void validate() {
        if (!enabled) return;
        if (!ipv4(mediaHost)
                || hookToken == null
                || !hookToken.matches("[A-Za-z0-9_-]{32,128}")
                || rtspPort < 1
                || rtspPort > 65535
                || hookPort < 1
                || hookPort > 65535
                || maxSessions < 1
                || maxSessions > 64
                || publishTokens == null
                || publishTokens.size() > 128
                || publishTokens.entrySet().stream()
                        .anyMatch(
                                e ->
                                        e.getKey() == null
                                                || !e.getKey().matches("[a-f0-9]{32}")
                                                || e.getValue() == null
                                                || e.getValue().length() < 32
                                                || e.getValue().length() > 256
                                                || !e.getValue().matches("[A-Za-z0-9_-]+")))
            throw new IllegalArgumentException("INGRESS_CONFIGURATION_INVALID");
        if (!gbEnabled) return;
        if (!ipv4(sipBindHost)
                || !ipv4(sipAdvertisedHost)
                || serverId == null
                || !serverId.matches("[0-9]{20}")
                || domain == null
                || !domain.matches("[0-9]{10}")
                || sipPort < 1
                || sipPort > 65535
                || rtpPortStart < 1024
                || rtpPortStart + 2 * maxSessions > 65535
                || (rtpTcpMode != 0 && rtpTcpMode != 1)
                || devices == null
                || devices.size() > 128)
            throw new IllegalArgumentException("GB_CONFIGURATION_INVALID");
        for (var entry : devices.entrySet()) {
            var device = entry.getValue();
            if (entry.getKey() == null
                    || !entry.getKey().matches("[0-9]{20}")
                    || device == null
                    || device.password == null
                    || device.password.length() < 8
                    || device.password.length() > 128
                    || device.remoteIps == null
                    || device.remoteIps.isEmpty()
                    || device.remoteIps.size() > 16
                    || device.remoteIps.stream().anyMatch(ip -> !ipv4(ip))
                    || device.channels == null
                    || device.channels.size() > 256
                    || device.channels.stream()
                            .anyMatch(id -> id == null || !id.matches("[0-9]{20}")))
                throw new IllegalArgumentException("GB_DEVICE_CONFIGURATION_INVALID");
        }
    }

    static boolean ipv4(String value) {
        if (value == null || !value.matches("(0|[1-9][0-9]{0,2})(\\.(0|[1-9][0-9]{0,2})){3}"))
            return false;
        for (String part : value.split("\\.")) if (Integer.parseInt(part) > 255) return false;
        return true;
    }
}
