package com.streamfusion.platform.media.preview.source;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraHttpTransport;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.media.preview.PreviewFailure;
import java.io.ByteArrayInputStream;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.time.Instant;
import java.util.Base64;
import java.util.Map;
import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilderFactory;
import org.springframework.stereotype.Component;

/** Uses the exact Media1/Media2 service and profile token saved during discovery. */
@Component
public class OnvifStreamLocatorAdapter implements StreamLocatorAdapter {
    private static final String MEDIA1 = "http://www.onvif.org/ver10/media/wsdl";
    private static final String MEDIA2 = "http://www.onvif.org/ver20/media/wsdl";
    private static final SecureRandom RANDOM = new SecureRandom();
    private final CameraHttpTransport transport;

    public OnvifStreamLocatorAdapter(CameraHttpTransport transport) {
        this.transport = transport;
    }

    @Override
    public String type() {
        return "ONVIF";
    }

    @Override
    public URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        String namespace = config.path("serviceNamespace").asText();
        String endpoint = config.path("serviceEndpoint").asText();
        String token = config.path("profileToken").asText();
        if ((!MEDIA1.equals(namespace) && !MEDIA2.equals(namespace))
                || endpoint.isBlank()
                || token.isBlank()) throw new PreviewFailure("LOCATOR_NOT_CONFIGURED");
        String content =
                MEDIA2.equals(namespace)
                        ? "<m:Protocol>RTSP</m:Protocol><m:ProfileToken>"
                                + xml(token)
                                + "</m:ProfileToken>"
                        : "<m:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></m:StreamSetup><m:ProfileToken>"
                                + xml(token)
                                + "</m:ProfileToken>";
        String envelope =
                "<s:Envelope xmlns:s=\"http://www.w3.org/2003/05/soap-envelope\" xmlns:m=\""
                        + namespace
                        + "\" xmlns:tt=\"http://www.onvif.org/ver10/schema\"><s:Header>"
                        + security(context)
                        + "</s:Header><s:Body><m:GetStreamUri>"
                        + content
                        + "</m:GetStreamUri></s:Body></s:Envelope>";
        byte[] response =
                transport
                        .open(context)
                        .post(
                                URI.create(endpoint),
                                envelope.getBytes(StandardCharsets.UTF_8),
                                "application/soap+xml; charset=utf-8; action=\""
                                        + namespace
                                        + "/GetStreamUri\"",
                                Map.of(),
                                true);
        try {
            var factory = DocumentBuilderFactory.newInstance();
            factory.setNamespaceAware(true);
            factory.setFeature(XMLConstants.FEATURE_SECURE_PROCESSING, true);
            factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
            factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "");
            factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
            var doc = factory.newDocumentBuilder().parse(new ByteArrayInputStream(response));
            if (doc.getElementsByTagNameNS("*", "Fault").getLength() > 0)
                throw new PreviewFailure("UPSTREAM_REJECTED");
            var values = doc.getElementsByTagNameNS("*", "Uri");
            if (values.getLength() != 1) throw new PreviewFailure("LOCATOR_INVALID");
            return transport.streamUri(context, values.item(0).getTextContent().strip());
        } catch (PreviewFailure ex) {
            throw ex;
        } catch (Exception ex) {
            throw new PreviewFailure("LOCATOR_INVALID");
        }
    }

    private static String security(CameraAccessContext context) {
        if (context.username() == null || context.username().isEmpty()) return "";
        byte[] nonce = new byte[20];
        RANDOM.nextBytes(nonce);
        String created = Instant.now().toString();
        try {
            var digest = MessageDigest.getInstance("SHA-1");
            digest.update(nonce);
            digest.update(created.getBytes(StandardCharsets.UTF_8));
            digest.update(context.password().getBytes(StandardCharsets.UTF_8));
            return "<wsse:Security s:mustUnderstand=\"1\" xmlns:wsse=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd\" xmlns:wsu=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd\"><wsse:UsernameToken><wsse:Username>"
                    + xml(context.username())
                    + "</wsse:Username><wsse:Password Type=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest\">"
                    + Base64.getEncoder().encodeToString(digest.digest())
                    + "</wsse:Password><wsse:Nonce EncodingType=\"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary\">"
                    + Base64.getEncoder().encodeToString(nonce)
                    + "</wsse:Nonce><wsu:Created>"
                    + created
                    + "</wsu:Created></wsse:UsernameToken></wsse:Security>";
        } catch (java.security.NoSuchAlgorithmException ex) {
            throw new IllegalStateException("SHA-1 unavailable");
        }
    }

    private static String xml(String value) {
        return value.replace("&", "&amp;")
                .replace("<", "&lt;")
                .replace(">", "&gt;")
                .replace("\"", "&quot;")
                .replace("'", "&apos;");
    }
}
