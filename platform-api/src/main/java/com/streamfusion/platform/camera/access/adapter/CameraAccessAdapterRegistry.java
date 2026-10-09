package com.streamfusion.platform.camera.access.adapter;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.springframework.stereotype.Component;

@Component
public class CameraAccessAdapterRegistry {
    private final Map<String, CameraAccessAdapter> adapters;
    private final List<CameraAdapterDescriptor> descriptors;
    private final List<String> methods;

    public List<CameraAdapterDescriptor> descriptors() {
        return descriptors;
    }

    public List<String> methods() {
        return methods;
    }

    public CameraAdapterDescriptor descriptor(String type) {
        return descriptors().stream()
                .filter(d -> d.type().equals(type))
                .findFirst()
                .orElseThrow(() -> new CameraAdapterException("PROTOCOL_NOT_SUPPORTED"));
    }

    public boolean supportsCategory(String type, String category) {
        return descriptors().stream()
                .anyMatch(d -> d.type().equals(type) && d.category().equals(category));
    }

    public CameraAccessAdapterRegistry(List<CameraAccessAdapter> implementations) {
        var entries = new LinkedHashMap<String, CameraAccessAdapter>();
        var metadata = new ArrayList<CameraAdapterDescriptor>();
        for (var adapter : implementations) {
            var descriptor = adapter.descriptor();
            validateDescriptor(adapter, descriptor);
            if (entries.put(adapter.type(), adapter) != null)
                throw new IllegalStateException("Duplicate camera adapter");
            metadata.add(descriptor);
        }
        adapters = Map.copyOf(entries);
        var registered =
                new ArrayList<>(
                        metadata.stream()
                                .sorted(
                                        Comparator.comparingInt(CameraAdapterDescriptor::autoOrder)
                                                .thenComparing(CameraAdapterDescriptor::type))
                                .toList());
        registered.add(
                new CameraAdapterDescriptor(
                        "RTSP", "已知 RTSP 地址", "RTSP", "RTSP_URL", false, 100, false));
        descriptors = List.copyOf(registered);
        var available = new ArrayList<String>();
        available.add("AUTO");
        descriptors.forEach(d -> available.add(d.type()));
        methods = List.copyOf(available);
    }

    private static void validateDescriptor(
            CameraAccessAdapter adapter, CameraAdapterDescriptor descriptor) {
        if (descriptor == null
                || descriptor.type() == null
                || !descriptor.type().equals(adapter.type())
                || !descriptor.type().matches("[A-Z][A-Z0-9_]{0,63}")
                || List.of("AUTO", "SCAN", "RTSP").contains(descriptor.type())
                || descriptor.label() == null
                || descriptor.label().isBlank())
            throw new IllegalStateException("Invalid camera adapter descriptor");

        boolean device = "DEVICE".equals(descriptor.category());
        boolean platform = "PLATFORM".equals(descriptor.category());
        String inputKind = device ? "DEVICE_LOGIN" : "PLATFORM_APPKEY";
        if (!device && !platform
                || !inputKind.equals(descriptor.inputKind())
                || descriptor.paged() && !platform
                || descriptor.autoDetect() && !device)
            throw new IllegalStateException("Invalid camera adapter descriptor");

        List<String> purposes =
                device ? List.of("DEVICE_HTTP", "VENDOR_HTTP", "ONVIF") : List.of("PLATFORM_HTTP");
        if (descriptor.endpointPath() == null
                || !descriptor.endpointPath().matches("(?:/[A-Za-z0-9_./-]*)?")
                || descriptor.endpointPurpose() == null
                || !purposes.contains(descriptor.endpointPurpose()))
            throw new IllegalStateException("Invalid camera adapter descriptor");
    }

    public CameraAccessCatalog discover(String type, CameraAccessContext context) {
        if (!"AUTO".equals(type)) return required(type).discover(context);
        CameraAccessCatalog partial = null;
        CameraAdapterException authenticationFailure = null;
        String fallbackWarning = null;
        for (String candidate :
                descriptors().stream()
                        .filter(CameraAdapterDescriptor::autoDetect)
                        .sorted(Comparator.comparingInt(CameraAdapterDescriptor::autoOrder))
                        .map(CameraAdapterDescriptor::type)
                        .toList()) {
            // A recognized vendor may use credentials independent of ONVIF. Try ONVIF once,
            // without retrying that vendor or sending credentials to other vendor protocols.
            if (fallbackWarning != null && !"ONVIF".equals(candidate)) continue;
            try {
                var catalog = required(candidate).discover(context);
                if (catalog.complete() || "ONVIF".equals(candidate)) {
                    if (partial != null && !catalog.complete()) return partial;
                    return fallbackWarning == null
                            ? catalog
                            : withWarning(catalog, fallbackWarning);
                }
                partial = catalog;
                fallbackWarning = "AUTO_NATIVE_CATALOG_PARTIAL";
            } catch (CameraAdapterException ex) {
                if ("AUTHENTICATION_FAILED".equals(ex.reasonCode())) {
                    authenticationFailure = ex;
                    fallbackWarning = "AUTO_NATIVE_AUTH_REJECTED";
                    continue;
                }
                if (!List.of("PROTOCOL_NOT_SUPPORTED", "ONVIF_MEDIA_NOT_SUPPORTED")
                        .contains(ex.reasonCode())) throw ex;
            }
        }
        if (partial != null) return partial;
        if (authenticationFailure != null) throw authenticationFailure;
        throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
    }

    private static CameraAccessCatalog withWarning(CameraAccessCatalog catalog, String warning) {
        var warnings = new ArrayList<>(catalog.warnings());
        warnings.add(warning);
        return new CameraAccessCatalog(
                catalog.adapterType(),
                catalog.device(),
                catalog.channels(),
                catalog.complete(),
                warnings,
                catalog.page(),
                catalog.observedAt());
    }

    private CameraAccessAdapter required(String type) {
        CameraAccessAdapter adapter = adapters.get(type);
        if (adapter == null) throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
        return adapter;
    }
}
