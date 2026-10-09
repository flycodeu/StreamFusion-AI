package com.streamfusion.platform.camera.config;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;

/** Deployment-owned keys and destination policies. Empty configuration disables camera writes. */
@Getter
@Setter
@Component
@ConfigurationProperties(prefix = "platform.camera")
public class CameraProperties {
    private String activeKeyId = "";
    private Map<String, String> keys = new LinkedHashMap<>();
    private Map<String, NetworkPolicy> networkPolicies = new LinkedHashMap<>();

    /** Public option labels only; destination rules remain server-side. */
    public List<Map<String, String>> networkPolicyOptions() {
        return networkPolicies.entrySet().stream()
                .map(
                        entry ->
                                Map.of(
                                        "key",
                                        entry.getKey(),
                                        "name",
                                        entry.getValue().getName() == null
                                                ? entry.getKey()
                                                : entry.getValue().getName()))
                .toList();
    }

    /** Selects the sole configured policy when omitted; callers still validate access. */
    public String defaultNetworkPolicyKey(String key) {
        return key == null && networkPolicies.size() == 1
                ? networkPolicies.keySet().iterator().next()
                : key;
    }

    @Getter
    @Setter
    public static class NetworkPolicy {
        private String name;
        private List<String> cidrs = List.of();
        private List<String> hosts = List.of();
    }
}
