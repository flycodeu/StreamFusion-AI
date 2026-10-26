package com.streamfusion.platform.camera.ingress;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.*;
import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilderFactory;
import org.w3c.dom.Document;

/** Bounded SIP datagrams and XXE-disabled MANSCDP XML. Header values never enter logs. */
record GbSipMessage(String start, Map<String, String> headers, byte[] body) {
    static GbSipMessage parse(byte[] bytes) {
        if (bytes.length > 65507) throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
        int split = -1;
        for (int i = 0; i + 3 < bytes.length; i++)
            if (bytes[i] == 13 && bytes[i + 1] == 10 && bytes[i + 2] == 13 && bytes[i + 3] == 10) {
                split = i;
                break;
            }
        if (split < 0 || split > 16384) throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
        String[] lines = new String(bytes, 0, split, StandardCharsets.US_ASCII).split("\r\n");
        if (lines.length < 6
                || lines.length > 64
                || !lines[0].matches(
                        "(?:[A-Z]+ sip:[^ ]+ SIP/2.0|SIP/2.0 [1-6][0-9]{2} [^\\r\\n]*)"))
            throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
        Map<String, String> headers = new LinkedHashMap<>();
        for (int i = 1; i < lines.length; i++) {
            int colon = lines[i].indexOf(':');
            if (colon < 1
                    || lines[i].length() > 2048
                    || lines[i].codePoints().anyMatch(c -> c < 32 && c != 9))
                throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
            String key = lines[i].substring(0, colon).toLowerCase(Locale.ROOT);
            if (headers.put(key, lines[i].substring(colon + 1).strip()) != null)
                throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
        }
        for (String key : List.of("via", "from", "to", "call-id", "cseq", "content-length"))
            if (!headers.containsKey(key))
                throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
        if (!headers.get("content-length").equals(Integer.toString(bytes.length - split - 4)))
            throw new IllegalArgumentException("SIP_MESSAGE_INVALID");
        return new GbSipMessage(
                lines[0], Map.copyOf(headers), Arrays.copyOfRange(bytes, split + 4, bytes.length));
    }

    String method() {
        return start.startsWith("SIP/2.0") ? "RESPONSE" : start.substring(0, start.indexOf(' '));
    }

    String header(String name) {
        return headers.getOrDefault(name, "");
    }

    String fromId() {
        var matcher = java.util.regex.Pattern.compile("sip:([0-9]{20})@").matcher(header("from"));
        return matcher.find() ? matcher.group(1) : "";
    }

    Document xml() {
        try {
            var factory = DocumentBuilderFactory.newInstance();
            factory.setFeature(XMLConstants.FEATURE_SECURE_PROCESSING, true);
            factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
            factory.setFeature("http://xml.org/sax/features/external-general-entities", false);
            factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false);
            factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "");
            factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
            factory.setAttribute("http://www.oracle.com/xml/jaxp/properties/maxElementDepth", "32");
            factory.setXIncludeAware(false);
            factory.setExpandEntityReferences(false);
            var builder = factory.newDocumentBuilder();
            builder.setErrorHandler(new org.xml.sax.helpers.DefaultHandler());
            return builder.parse(new ByteArrayInputStream(body));
        } catch (Exception failure) {
            throw new IllegalArgumentException("GB_XML_INVALID");
        }
    }

    static String text(Document xml, String tag) {
        var nodes = xml.getElementsByTagName(tag);
        return nodes.getLength() == 0 ? "" : nodes.item(0).getTextContent().strip();
    }

    @Override
    public String toString() {
        return "GbSipMessage[redacted]";
    }
}
