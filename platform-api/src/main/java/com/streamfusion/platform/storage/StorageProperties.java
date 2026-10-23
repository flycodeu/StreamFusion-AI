package com.streamfusion.platform.storage;

import java.util.List;
import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;

/** Deployment-owned storage destinations; camera policies and task routing are independent. */
@Getter
@Setter
@Component
@ConfigurationProperties(prefix = "platform.storage")
public class StorageProperties {
    private List<String> allowedEndpoints =
            List.of("http://127.0.0.1:8333", "http://localhost:8333");
}
