package com.streamfusion.platform.camera.access.adapter;

import java.net.URI;
import java.time.Instant;
import java.util.List;

/**
 * Internal normalized observations. URI fields can contain secrets and must not become public VOs.
 */
public record CameraAccessCatalog(
        String adapterType,
        Device device,
        List<Channel> channels,
        boolean complete,
        List<String> warnings,
        Page page,
        Instant observedAt) {
    public CameraAccessCatalog(
            String adapterType,
            Device device,
            List<Channel> channels,
            boolean complete,
            List<String> warnings,
            Page page) {
        this(adapterType, device, channels, complete, warnings, page, null);
    }

    public CameraAccessCatalog observedAt(Instant time) {
        return new CameraAccessCatalog(
                adapterType, device, channels, complete, warnings, page, time);
    }

    public CameraAccessCatalog(
            String adapterType,
            Device device,
            List<Channel> channels,
            boolean complete,
            List<String> warnings) {
        this(adapterType, device, channels, complete, warnings, null);
    }

    public record Page(int pageNumber, int pageSize, Long total, boolean hasMore) {}

    public CameraAccessCatalog {
        channels = List.copyOf(channels);
        warnings = List.copyOf(warnings);
        if (channels.size() > 256
                || channels.stream().anyMatch(channel -> channel.profiles().size() > 8))
            throw new CameraAdapterException("DISCOVERY_LIMIT_EXCEEDED");
    }

    /** Platform pages may contain channels owned by different devices. */
    public Device deviceFor(Channel channel) {
        return channel.device() == null ? device : channel.device();
    }

    public record Device(
            String externalKey,
            String name,
            String manufacturer,
            String model,
            String firmware,
            String serialNumber) {}

    public record Channel(
            String externalKey,
            String name,
            List<Profile> profiles,
            boolean mappingRequired,
            Device device) {
        public Channel(
                String externalKey, String name, List<Profile> profiles, boolean mappingRequired) {
            this(externalKey, name, profiles, mappingRequired, null);
        }

        public Channel {
            profiles = List.copyOf(profiles);
        }
    }

    public record Profile(
            String externalKey,
            String name,
            String usageHint,
            String codec,
            Integer width,
            Integer height,
            Double frameRate,
            Integer bitrateKbps,
            Locator locator) {}

    public record Locator(
            String kind,
            URI uri,
            String profileToken,
            Integer streamType,
            String serviceNamespace,
            URI serviceEndpoint) {
        public Locator(String kind, URI uri, String profileToken, Integer streamType) {
            this(kind, uri, profileToken, streamType, null, null);
        }

        @Override
        public String toString() {
            return "Locator[kind=" + kind + ",redacted]";
        }
    }
}
