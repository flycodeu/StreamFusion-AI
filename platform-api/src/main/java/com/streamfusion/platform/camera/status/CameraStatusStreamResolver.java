package com.streamfusion.platform.camera.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.source.DahuaStreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.HikvisionStreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.OnvifStreamLocatorAdapter;
import java.net.URI;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Reuses preview's protocol identities; only the worker resolves ONVIF over the network. */
@Component
@RequiredArgsConstructor
public class CameraStatusStreamResolver {
    private final OnvifStreamLocatorAdapter onvif;
    private final HikvisionStreamLocatorAdapter hikvision;
    private final DahuaStreamLocatorAdapter dahua;

    public static boolean supports(String kind) {
        return "ONVIF".equals(kind)
                || "HIKVISION".equals(kind)
                || "DAHUA".equals(kind)
                || "HIK_PLATFORM".equals(kind);
    }

    public URI configuredUri(
            CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        return switch (locator.getLocatorKind()) {
            case "HIKVISION" -> hikvision.resolve(locator, config, context);
            case "DAHUA" -> dahua.resolve(locator, config, context);
            case "ONVIF" -> {
                validateOnvif(config, context);
                String host = context.endpoint().getHost();
                // Identity only: this URI is replaced by GetStreamUri before any RTSP request.
                yield URI.create("rtsp://" + host + ":" + context.rtspPort() + "/");
            }
            default -> throw new PreviewFailure("SOURCE_PROTOCOL_NOT_SUPPORTED");
        };
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
                uri = onvif.resolve(locator, config, execution);
                validateRtsp(uri);
            } catch (PreviewFailure ex) {
                throw new CameraAdapterException("STREAM_URI_INVALID");
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
            case "INVALID_STREAM_URI" -> "STREAM_URI_INVALID";
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
