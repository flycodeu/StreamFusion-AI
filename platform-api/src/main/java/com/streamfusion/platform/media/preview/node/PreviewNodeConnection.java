package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;

/** Registration input from an administrator-owned file; the browser never handles machine keys. */
public record PreviewNodeConnection(
        String nodeId,
        String name,
        URI controlUri,
        String certificateSha256,
        Path tokenFile,
        String mediaAddress) {
    public PreviewNodeConnection {
        if (nodeId == null
                || nodeId.equals("local")
                || !nodeId.matches("[A-Za-z0-9_-]{1,64}")
                || name == null
                || name.isBlank()
                || name.length() > 80
                || controlUri == null
                || !"https".equals(controlUri.getScheme())
                || controlUri.getHost() == null
                || controlUri.getRawUserInfo() != null
                || !controlUri.getRawPath().isEmpty()
                || controlUri.getQuery() != null
                || controlUri.getFragment() != null
                || certificateSha256 == null
                || !certificateSha256.matches("[a-fA-F0-9]{64}")
                || tokenFile == null
                || !tokenFile.isAbsolute()
                || mediaAddress == null
                || !mediaAddress.matches("[A-Za-z0-9.:-]{1,253}"))
            throw new IllegalArgumentException("Invalid preview node registration");
    }

    public static java.util.List<PreviewNodeConnection> readAll(Path path, ObjectMapper json) {
        try {
            if (Files.size(path) > 262144) throw new IllegalArgumentException();
            var tree = json.readTree(Files.readAllBytes(path));
            var values = new java.util.ArrayList<PreviewNodeConnection>();
            if (tree.isArray())
                for (var item : tree)
                    values.add(json.treeToValue(item, PreviewNodeConnection.class));
            else values.add(json.treeToValue(tree, PreviewNodeConnection.class));
            if (values.isEmpty()
                    || values.size() > 16
                    || values.stream().map(PreviewNodeConnection::nodeId).distinct().count()
                            != values.size()) throw new IllegalArgumentException();
            return java.util.List.copyOf(values);
        } catch (Exception ex) {
            throw new com.streamfusion.platform.media.preview.PreviewFailure(
                    "NODE_REGISTRATION_INVALID");
        }
    }

    public static PreviewNodeConnection read(Path path, ObjectMapper json) {
        try {
            if (Files.size(path) > 16384) throw new IllegalArgumentException();
            return json.readValue(Files.readAllBytes(path), PreviewNodeConnection.class);
        } catch (Exception ex) {
            throw new com.streamfusion.platform.media.preview.PreviewFailure(
                    "NODE_REGISTRATION_INVALID");
        }
    }

    @Override
    public String toString() {
        return "PreviewNodeConnection[nodeId=" + nodeId + "]";
    }
}
