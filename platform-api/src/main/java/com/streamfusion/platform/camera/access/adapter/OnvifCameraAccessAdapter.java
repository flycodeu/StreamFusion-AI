package com.streamfusion.platform.camera.access.adapter;

import static com.streamfusion.platform.camera.access.adapter.CameraXml.*;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import org.springframework.stereotype.Component;
import org.w3c.dom.Document;
import org.w3c.dom.Element;

/**
 * ONVIF Media1/Media2 read-only discovery with one selected service and a shared catalog mapper.
 */
@Component
public class OnvifCameraAccessAdapter implements CameraAccessAdapter {
    private static final String DEVICE = "http://www.onvif.org/ver10/device/wsdl";
    private static final String MEDIA = "http://www.onvif.org/ver10/media/wsdl";
    private static final String MEDIA2 = "http://www.onvif.org/ver20/media/wsdl";
    private static final SecureRandom RANDOM = new SecureRandom();
    private final CameraHttpTransport transport;

    public OnvifCameraAccessAdapter(CameraHttpTransport transport) {
        this.transport = transport;
    }

    @Override
    public String type() {
        return "ONVIF";
    }

    @Override
    public CameraAdapterDescriptor descriptor() {
        return new CameraAdapterDescriptor(
                type(),
                "ONVIF 设备",
                "DEVICE",
                "DEVICE_LOGIN",
                true,
                30,
                false,
                "/onvif/device_service",
                "ONVIF");
    }

    @Override
    public CameraAccessCatalog discover(CameraAccessContext context) {
        var session = transport.open(context);
        URI deviceEndpoint = context.endpoint();
        if (deviceEndpoint.getPath().isEmpty() || deviceEndpoint.getPath().equals("/"))
            deviceEndpoint = deviceEndpoint.resolve("/onvif/device_service");
        Document info = soap(session, context, deviceEndpoint, DEVICE, "GetDeviceInformation", "");
        var warnings = new LinkedHashSet<String>();
        var device =
                CameraCatalogSupport.device(
                        text(info, "Model"),
                        text(info, "Manufacturer"),
                        text(info, "Model"),
                        text(info, "FirmwareVersion"),
                        text(info, "SerialNumber"),
                        warnings);
        MediaService media = mediaEndpoint(session, context, deviceEndpoint);
        Document profiles =
                soap(
                        session,
                        context,
                        media.endpoint(),
                        media.namespace(),
                        "GetProfiles",
                        media.isMedia2() ? "<tr2:Type>All</tr2:Type>" : "");
        var elements = all(profiles, "Profiles");
        if (elements.size() > 2048) throw new CameraAdapterException("DISCOVERY_LIMIT_EXCEEDED");
        Map<String, List<CameraAccessCatalog.Profile>> grouped = new LinkedHashMap<>();
        Map<String, Boolean> mapping = new LinkedHashMap<>();
        var tokens = new LinkedHashSet<String>();
        for (Element profile : elements) {
            String token = clean(profile.getAttribute("token"));
            if (token == null || token.length() > 512 || !tokens.add(token))
                throw new CameraAdapterException("INVALID_PROFILE_IDENTITY");
            Element configurations = media.isMedia2() ? first(profile, "Configurations") : profile;
            Element source =
                    configurations == null
                            ? null
                            : first(
                                    configurations,
                                    media.isMedia2() ? "VideoSource" : "VideoSourceConfiguration");
            Element encoder =
                    configurations == null
                            ? null
                            : first(
                                    configurations,
                                    media.isMedia2()
                                            ? "VideoEncoder"
                                            : "VideoEncoderConfiguration");
            if (source == null || encoder == null) {
                warnings.add("NON_VIDEO_PROFILE_SKIPPED");
                continue;
            }
            String sourceToken = text(source, "SourceToken");
            Element bounds = first(source, "Bounds");
            String view =
                    sourceToken == null
                            ? "unmapped:" + token
                            : sourceToken
                                    + "|"
                                    + (bounds == null
                                            ? ""
                                            : bounds.getAttribute("x")
                                                    + ","
                                                    + bounds.getAttribute("y")
                                                    + ","
                                                    + bounds.getAttribute("width")
                                                    + ","
                                                    + bounds.getAttribute("height"))
                                    + "|"
                                    + text(source, "Mode")
                                    + "|"
                                    + text(source, "Degree");
            String channelKey =
                    "onvif-view:"
                            + CameraHttpAuthentication.hash(
                                    "SHA-256", view, StandardCharsets.UTF_8);
            boolean unresolved = sourceToken == null;
            if (unresolved) warnings.add("CHANNEL_MAPPING_REQUIRED");
            // Archive discovery records the control identity; it never asks for a media URL.
            var locator =
                    new CameraAccessCatalog.Locator(
                            "ONVIF", null, token, null, media.namespace(), media.endpoint());
            var normalized =
                    new CameraAccessCatalog.Profile(
                            token,
                            text(profile, "Name"),
                            "UNKNOWN",
                            text(encoder, "Encoding"),
                            integer(text(encoder, "Width")),
                            integer(text(encoder, "Height")),
                            decimal(text(encoder, "FrameRateLimit")),
                            integer(text(encoder, "BitrateLimit")),
                            locator);
            grouped.computeIfAbsent(channelKey, ignored -> new ArrayList<>()).add(normalized);
            mapping.put(channelKey, unresolved);
        }
        if (grouped.isEmpty()) warnings.add("NO_CHANNEL_IDENTITIES");
        var channels = new ArrayList<CameraAccessCatalog.Channel>();
        grouped.forEach(
                (key, values) ->
                        channels.add(
                                new CameraAccessCatalog.Channel(
                                        key, values.getFirst().name(), values, mapping.get(key))));
        boolean complete = !warnings.contains("CHANNEL_MAPPING_REQUIRED");
        return new CameraAccessCatalog(type(), device, channels, complete, List.copyOf(warnings));
    }

    private MediaService mediaEndpoint(
            CameraHttpTransport.Session session, CameraAccessContext context, URI device) {
        try {
            Document services =
                    soap(
                            session,
                            context,
                            device,
                            DEVICE,
                            "GetServices",
                            "<tds:IncludeCapability>false</tds:IncludeCapability>");
            MediaService media1 = null;
            for (Element service : all(services, "Service")) {
                if (MEDIA2.equals(text(service, "Namespace")))
                    return new MediaService(endpoint(device, text(service, "XAddr")), MEDIA2);
                if (MEDIA.equals(text(service, "Namespace")))
                    media1 = new MediaService(endpoint(device, text(service, "XAddr")), MEDIA);
            }
            if (media1 != null) return media1;
        } catch (CameraAdapterException ex) {
            if (!List.of(
                            "PROTOCOL_NOT_SUPPORTED",
                            "SOAP_ACTION_NOT_SUPPORTED",
                            "UPSTREAM_HTTP_ERROR")
                    .contains(ex.reasonCode())) throw ex;
        }
        Document capabilities =
                soap(
                        session,
                        context,
                        device,
                        DEVICE,
                        "GetCapabilities",
                        "<tds:Category>Media</tds:Category>");
        Element media = first(capabilities, "Media");
        String address = text(media, "XAddr");
        if (address == null) throw new CameraAdapterException("ONVIF_MEDIA_NOT_SUPPORTED");
        return new MediaService(endpoint(device, address), MEDIA);
    }

    private URI endpoint(URI base, String value) {
        if (value == null) throw new CameraAdapterException("INVALID_SERVICE_ADDRESS");
        try {
            return base.resolve(value);
        } catch (IllegalArgumentException ex) {
            throw new CameraAdapterException("INVALID_SERVICE_ADDRESS");
        }
    }

    private Document soap(
            CameraHttpTransport.Session session,
            CameraAccessContext context,
            URI endpoint,
            String namespace,
            String action,
            String contents) {
        String prefix = namespace.equals(DEVICE) ? "tds" : namespace.equals(MEDIA2) ? "tr2" : "trt";
        String envelope =
                "<s:Envelope xmlns:s=\"http://www.w3.org/2003/05/soap-envelope\""
                        + " xmlns:tds=\""
                        + DEVICE
                        + "\" xmlns:trt=\""
                        + MEDIA
                        + "\" xmlns:tr2=\""
                        + MEDIA2
                        + "\""
                        + " xmlns:tt=\"http://www.onvif.org/ver10/schema\"><s:Header>"
                        + security(context)
                        + "</s:Header><s:Body><"
                        + prefix
                        + ":"
                        + action
                        + ">"
                        + contents
                        + "</"
                        + prefix
                        + ":"
                        + action
                        + "></s:Body></s:Envelope>";
        var document =
                parse(
                        session.post(
                                endpoint,
                                envelope.getBytes(StandardCharsets.UTF_8),
                                "application/soap+xml; charset=utf-8; action=\""
                                        + namespace
                                        + "/"
                                        + action
                                        + "\"",
                                Map.of(),
                                true));
        checkSoapFault(document);
        return document;
    }

    private record MediaService(URI endpoint, String namespace) {
        boolean isMedia2() {
            return MEDIA2.equals(namespace);
        }
    }

    private String security(CameraAccessContext context) {
        if (context.username() == null || context.username().isEmpty()) return "";
        byte[] nonce = new byte[20];
        RANDOM.nextBytes(nonce);
        String created = Instant.now().toString();
        try {
            var sha1 = MessageDigest.getInstance("SHA-1");
            sha1.update(nonce);
            sha1.update(created.getBytes(StandardCharsets.UTF_8));
            sha1.update(context.password().getBytes(StandardCharsets.UTF_8));
            return "<wsse:Security s:mustUnderstand=\"1\" xmlns:wsse=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd\""
                    + " xmlns:wsu=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd\">"
                    + "<wsse:UsernameToken><wsse:Username>"
                    + escape(context.username())
                    + "</wsse:Username>"
                    + "<wsse:Password Type=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest\">"
                    + Base64.getEncoder().encodeToString(sha1.digest())
                    + "</wsse:Password>"
                    + "<wsse:Nonce EncodingType=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary\">"
                    + Base64.getEncoder().encodeToString(nonce)
                    + "</wsse:Nonce><wsu:Created>"
                    + created
                    + "</wsu:Created></wsse:UsernameToken></wsse:Security>";
        } catch (java.security.NoSuchAlgorithmException ex) {
            throw new IllegalStateException("Required digest unavailable");
        }
    }
}
