package com.streamfusion.platform.camera.access.adapter;

import java.io.ByteArrayInputStream;
import java.util.ArrayList;
import java.util.List;
import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilderFactory;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.Node;
import org.xml.sax.SAXParseException;
import org.xml.sax.helpers.DefaultHandler;

final class CameraXml {
    private CameraXml() {}

    static Document parse(byte[] bytes) {
        try {
            var factory = DocumentBuilderFactory.newInstance();
            factory.setNamespaceAware(true);
            factory.setFeature(XMLConstants.FEATURE_SECURE_PROCESSING, true);
            factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
            factory.setFeature("http://xml.org/sax/features/external-general-entities", false);
            factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false);
            factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "");
            factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
            factory.setAttribute("http://www.oracle.com/xml/jaxp/properties/maxElementDepth", "64");
            factory.setXIncludeAware(false);
            factory.setExpandEntityReferences(false);
            var builder = factory.newDocumentBuilder();
            builder.setErrorHandler(
                    new DefaultHandler() {
                        @Override
                        public void fatalError(SAXParseException e) throws SAXParseException {
                            throw e;
                        }

                        @Override
                        public void error(SAXParseException e) throws SAXParseException {
                            throw e;
                        }
                    });
            return builder.parse(new ByteArrayInputStream(bytes));
        } catch (Exception ex) {
            throw new CameraAdapterException("INVALID_XML_RESPONSE");
        }
    }

    static List<Element> all(Node node, String name) {
        var found =
                node instanceof Document doc
                        ? doc.getElementsByTagNameNS("*", name)
                        : ((Element) node).getElementsByTagNameNS("*", name);
        List<Element> result = new ArrayList<>();
        for (int i = 0; i < found.getLength(); i++) result.add((Element) found.item(i));
        return result;
    }

    static Element first(Node node, String name) {
        var found = all(node, name);
        return found.isEmpty() ? null : found.getFirst();
    }

    static String text(Node node, String name) {
        if (node == null) return null;
        Element element = first(node, name);
        return element == null ? null : clean(element.getTextContent());
    }

    static String child(Element node, String name) {
        if (node == null) return null;
        for (Node child = node.getFirstChild(); child != null; child = child.getNextSibling()) {
            if (child instanceof Element el && name.equals(el.getLocalName()))
                return clean(el.getTextContent());
        }
        return null;
    }

    static String clean(String value) {
        if (value == null || value.isBlank()) return null;
        String text = value.strip();
        if (text.length() > 2048 || text.codePoints().anyMatch(Character::isISOControl))
            throw new CameraAdapterException("INVALID_PROTOCOL_FIELD");
        return text;
    }

    static Integer integer(String value) {
        if (value == null) return null;
        try {
            int parsed = Integer.parseInt(value);
            return parsed < 0 ? null : parsed;
        } catch (NumberFormatException ex) {
            return null;
        }
    }

    static Double decimal(String value) {
        if (value == null) return null;
        try {
            double parsed = Double.parseDouble(value);
            return !Double.isFinite(parsed) || parsed < 0 ? null : parsed;
        } catch (NumberFormatException ex) {
            return null;
        }
    }

    static String escape(String value) {
        if (value == null) return "";
        return value.replace("&", "&amp;")
                .replace("<", "&lt;")
                .replace(">", "&gt;")
                .replace("\"", "&quot;")
                .replace("'", "&apos;");
    }

    static void checkSoapFault(Document document) {
        Element fault = first(document, "Fault");
        if (fault == null) return;
        var codes =
                all(fault, "Value").stream().map(value -> value.getTextContent().strip()).toList();
        if (codes.stream()
                .anyMatch(
                        code ->
                                code.endsWith(":NotAuthorized")
                                        || code.endsWith(":FailedAuthentication")
                                        || code.endsWith(":InvalidSecurity")))
            throw new CameraAdapterException("AUTHENTICATION_FAILED");
        if (codes.stream().anyMatch(code -> code.endsWith(":ActionNotSupported")))
            throw new CameraAdapterException("SOAP_ACTION_NOT_SUPPORTED");
        throw new CameraAdapterException("ONVIF_SOAP_FAULT");
    }
}
