package com.streamfusion.platform.media.preview.source;

import com.streamfusion.platform.media.preview.PreviewFailure;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.springframework.stereotype.Component;

/** One validated locator set shared by preview and read-only status qualification. */
@Component
public class StreamLocatorAdapterRegistry {
    private final Map<String, StreamLocatorAdapter> adapters;

    public StreamLocatorAdapterRegistry(List<StreamLocatorAdapter> implementations) {
        var registered = new LinkedHashMap<String, StreamLocatorAdapter>();
        for (var adapter : implementations) {
            String type = adapter.type();
            if (type == null || !type.matches("[A-Z][A-Z0-9_]{0,63}"))
                throw new IllegalStateException("Invalid stream locator adapter");
            if (registered.put(type, adapter) != null)
                throw new IllegalStateException("Duplicate stream locator adapter");
        }
        adapters = Map.copyOf(registered);
    }

    public boolean supports(String kind) {
        return kind != null && adapters.containsKey(kind);
    }

    public StreamLocatorAdapter required(String kind) {
        if (!supports(kind)) throw new PreviewFailure("SOURCE_PROTOCOL_NOT_SUPPORTED");
        return adapters.get(kind);
    }
}
