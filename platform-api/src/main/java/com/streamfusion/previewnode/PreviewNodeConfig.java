package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.control.PreviewControlTls;
import com.streamfusion.platform.media.preview.execution.PreviewRuntimeProvider;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveFfmpeg;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.List;
import java.util.Optional;

/** Deployment-owned configuration. Paths and credentials never leave this process. */
public record PreviewNodeConfig(
        String nodeId,
        String bindHost,
        int controlPort,
        Path keyStore,
        Path keyStorePasswordFile,
        Path tokenFile,
        Path workDir,
        URI zlmApi,
        Path zlmExecutable,
        Path zlmConfig,
        Path zlmSecretFile,
        Path ffmpegExecutable,
        String mediaAddress,
        List<String> allowedTargets,
        int maxSessions,
        int maxTranscodes,
        com.streamfusion.platform.media.preview.control.PreviewBudget budget)
        implements PreviewRuntimeProvider {
    public PreviewNodeConfig {
        if (nodeId == null
                || !nodeId.matches("[a-zA-Z0-9_-]{1,64}")
                || bindHost == null
                || controlPort < 1
                || controlPort > 65535
                || keyStore == null
                || keyStorePasswordFile == null
                || tokenFile == null
                || workDir == null
                || zlmApi == null
                || !"http".equals(zlmApi.getScheme())
                || !List.of("127.0.0.1", "[::1]").contains(zlmApi.getHost())
                || zlmApi.getPort() < 1
                || zlmApi.getUserInfo() != null
                || !"".equals(zlmApi.getRawPath())
                || zlmApi.getQuery() != null
                || zlmApi.getFragment() != null
                || zlmExecutable == null
                || zlmConfig == null
                || zlmSecretFile == null
                || ffmpegExecutable == null
                || mediaAddress == null
                || !mediaAddress.matches("[a-zA-Z0-9.:-]{1,253}")
                || allowedTargets == null
                || allowedTargets.isEmpty()
                || allowedTargets.size() > 256)
            throw new IllegalArgumentException("Invalid preview node configuration");
        new PreviewProperties(maxSessions, maxTranscodes);
        if (budget == null)
            budget =
                    com.streamfusion.platform.media.preview.control.PreviewBudget.defaults(
                            maxSessions, maxTranscodes);
        if (budget.maxViewers() != maxSessions || budget.maxTranscodes() != maxTranscodes)
            throw new IllegalArgumentException("Inconsistent preview budgets");
        if (List.of(
                        keyStore,
                        keyStorePasswordFile,
                        tokenFile,
                        workDir,
                        zlmExecutable,
                        zlmConfig,
                        zlmSecretFile,
                        ffmpegExecutable)
                .stream()
                .anyMatch(path -> !path.isAbsolute()))
            throw new IllegalArgumentException("Node paths must be absolute");
        allowedTargets = List.copyOf(allowedTargets);
        for (String target : allowedTargets) PreviewNodeSourcePolicy.target(target);
    }

    public static PreviewNodeConfig read(Path path, ObjectMapper json) throws Exception {
        if (Files.size(path) > 65536)
            throw new IllegalArgumentException("Node configuration too large");
        return json.readValue(Files.readAllBytes(path), PreviewNodeConfig.class);
    }

    public String version(ObjectMapper json) {
        try {
            if (Files.size(zlmConfig) > 262144) throw new IllegalArgumentException();
            var digest = MessageDigest.getInstance("SHA-256");
            digest.update(json.writeValueAsBytes(this));
            digest.update(Files.readAllBytes(zlmConfig));
            return HexFormat.of().formatHex(digest.digest());
        } catch (Exception ex) {
            throw new IllegalStateException("Cannot fingerprint node configuration");
        }
    }

    public Optional<ActiveZlm> activeZlm() {
        return Optional.of(
                new ActiveZlm(
                        zlmApi,
                        PreviewControlTls.secret(zlmSecretFile),
                        zlmExecutable,
                        zlmConfig,
                        "managed"));
    }

    public void requireFiles() {
        if (!Files.isRegularFile(zlmExecutable)
                || !Files.isExecutable(zlmExecutable)
                || !Files.isRegularFile(ffmpegExecutable)
                || !Files.isExecutable(ffmpegExecutable)
                || !Files.isRegularFile(zlmConfig))
            throw new IllegalArgumentException("Node media runtime files are missing");
    }

    public Optional<ActiveFfmpeg> activeFfmpeg() {
        return Optional.of(new ActiveFfmpeg(ffmpegExecutable, "managed"));
    }

    public Optional<ActiveZlm> recoveryZlm(String api, String configPath) {
        return activeZlm()
                .filter(
                        value ->
                                value.apiBase().toString().equals(api)
                                        && value.configPath()
                                                .toAbsolutePath()
                                                .normalize()
                                                .toString()
                                                .equals(configPath));
    }

    @Override
    public String toString() {
        return "PreviewNodeConfig[nodeId=" + nodeId + "]";
    }
}
