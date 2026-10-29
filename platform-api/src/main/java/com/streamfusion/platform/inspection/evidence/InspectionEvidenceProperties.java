package com.streamfusion.platform.inspection.evidence;

import jakarta.annotation.PostConstruct;
import java.util.HashSet;
import java.util.Map;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;

/**
 * Deployment limits and per-node inbound credentials. Unconfigured evidence upload stays closed.
 */
@Component
@ConfigurationProperties(prefix = "platform.inspection.evidence")
public class InspectionEvidenceProperties {
    private Map<String, String> nodeTokens = Map.of();
    private long maxAccountedBytesPerScope;

    public Map<String, String> getNodeTokens() {
        return nodeTokens;
    }

    public void setNodeTokens(Map<String, String> nodeTokens) {
        this.nodeTokens = nodeTokens;
    }

    public long getMaxAccountedBytesPerScope() {
        return maxAccountedBytesPerScope;
    }

    public void setMaxAccountedBytesPerScope(long maxAccountedBytesPerScope) {
        this.maxAccountedBytesPerScope = maxAccountedBytesPerScope;
    }

    @PostConstruct
    void validate() {
        if (nodeTokens == null || nodeTokens.size() > 100 || maxAccountedBytesPerScope < 0)
            throw new IllegalStateException("Invalid inspection evidence configuration");
        var unique = new HashSet<String>();
        for (var entry : nodeTokens.entrySet()) {
            String token = entry.getValue();
            if (entry.getKey() == null
                    || !entry.getKey().matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,63}")
                    || token == null
                    || token.length() < 32
                    || token.length() > 256
                    || !token.chars().allMatch(c -> c >= 33 && c <= 126)
                    || !unique.add(token))
                throw new IllegalStateException("Invalid inspection node credentials");
        }
        nodeTokens = Map.copyOf(nodeTokens);
    }
}
