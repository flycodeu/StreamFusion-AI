package com.streamfusion.platform.camera.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapterRegistry;
import java.net.URI;
import org.springframework.stereotype.Component;

/** Reuses preview's protocol identities; only the worker resolves ONVIF over the network. */
@Component
public class CameraStatusStreamResolver {
    private final StreamLocatorAdapterRegistry adapters;

    public CameraStatusStreamResolver(StreamLocatorAdapterRegistry adapters) {
        this.adapters = adapters;
    }

    public boolean supports(String kind) {
        return adapters.supports(kind);
    }

    public URI configuredUri(
            CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        var adapter = required(locator.getLocatorKind());
        if ("ONVIF".equals(locator.getLocatorKind())) {
            validateOnvif(config, context);
            String host = context.endpoint().getHost();
            // Identity only: GetStreamUri runs in the worker, never during read-only inspection.
            return URI.create("rtsp://" + host + ":" + context.rtspPort() + "/");
        }
        return adapter.resolve(locator, config, context);
    }

    public CameraStatusWorker.TargetResolver deferred(
            CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        if (!"ONVIF".equals(locator.getLocatorKind())) return null;
        return (deadline, checkActive) -> {
            checkActive.run();
            var execution =
                    new CameraAccessContext(
                            context.endpoint(),
                            context.networkPolicyKey(),
                            context.username(),
                            context.password(),
                            context.rtspPort(),
                            checkActive,
                            deadline);
            URI uri;
            try {
                uri = required(locator.getLocatorKind()).resolve(locator, config, execution);
                validateRtsp(uri);
            } catch (PreviewFailure ex) {
                throw new CameraAdapterException(resolutionReason(ex.reason()));
            } catch (CameraAdapterException ex) {
                throw new CameraAdapterException(resolutionReason(ex.reasonCode()));
            }
            checkActive.run();
            return new RtspDescribeProbe.Target(
                    uri,
                    context.networkPolicyKey(),
                    context.username(),
                    context.password(),
                    context.checkAuthorization());
        };
    }

    private StreamLocatorAdapter required(String kind) {
        return adapters.required(kind);
    }

    public static void validateRtsp(URI uri) {
        if (uri == null
                || !"rtsp".equalsIgnoreCase(uri.getScheme())
                || uri.getHost() == null
                || uri.getRawUserInfo() != null
                || uri.getRawFragment() != null
                || uri.toASCIIString().length() > 8192
                || uri.getPort() == 0
                || uri.getPort() > 65535) throw new PreviewFailure("LOCATOR_INVALID");
    }

    private static void validateOnvif(JsonNode config, CameraAccessContext context) {
        String namespace = config.path("serviceNamespace").asText();
        if (config.path("profileToken").asText().isBlank()
                || config.path("serviceEndpoint").asText().isBlank()
                || !("http://www.onvif.org/ver10/media/wsdl".equals(namespace)
                        || "http://www.onvif.org/ver20/media/wsdl".equals(namespace)))
            throw new PreviewFailure("ONVIF_MEDIA_NOT_CONFIGURED");
        URI service = URI.create(config.path("serviceEndpoint").asText());
        if (service.getHost() == null
                || service.getRawUserInfo() != null
                || service.getFragment() != null
                || service.toASCIIString().length() > 8192
                || service.getPort() == 0
                || service.getPort() > 65535
                || !("http".equals(service.getScheme()) || "https".equals(service.getScheme()))
                || !service.getHost().equalsIgnoreCase(context.endpoint().getHost())
                || ("https".equals(context.endpoint().getScheme())
                        && !"https".equals(service.getScheme())))
            throw new PreviewFailure("LOCATOR_INVALID");
    }

    private static String resolutionReason(String reason) {
        return switch (reason == null ? "" : reason) {
            case "AUTHENTICATION_FAILED" -> "STREAM_URI_AUTH_REJECTED";
            case "CONNECTION_TIMEOUT", "DISCOVERY_TIMEOUT" -> "STREAM_URI_TIMEOUT";
            case "DESTINATION_REJECTED", "CREDENTIAL_ORIGIN_REJECTED" -> "NETWORK_BLOCKED";
            case "INVALID_STREAM_URI", "LOCATOR_INVALID" -> "STREAM_URI_INVALID";
            case "ACTOR_REVOKED",
                    "CONFIGURATION_CHANGED",
                    "CONFIGURATION_UNAVAILABLE",
                    "TARGET_DELETED",
                    "CHECK_INTERRUPTED",
                    "CHECK_NO_LONGER_ACTIVE",
                    "CHECK_EXPIRED",
                    "SESSION_STORE_UNAVAILABLE",
                    "CANCELLED",
                    "STREAM_URI_TIMEOUT" ->
                    reason;
            default -> "STREAM_URI_UNAVAILABLE";
        };
    }
}
