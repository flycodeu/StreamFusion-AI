package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.control.PreviewControlTls;
import com.streamfusion.platform.media.preview.execution.LocalPreviewExecutor;
import com.streamfusion.platform.media.preview.execution.PreviewRecoveryCoordinator;
import com.streamfusion.platform.media.preview.media.PreviewProcessInspector;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import com.streamfusion.platform.media.preview.media.PreviewTranscodeRegistry;
import com.streamfusion.platform.media.preview.media.ZlmPreviewGateway;
import java.net.InetSocketAddress;
import java.nio.file.Path;
import java.time.Clock;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;

/** Optional standalone preview entry point. Deliberately does not start SpringApplication. */
public final class PreviewNodeApplication {
    private PreviewNodeApplication() {}

    public static void main(String[] args) throws Exception {
        if (args.length == 1 && args[0].equals("--help")) {
            System.out.println(
                    "Usage: java -jar preview-node.jar /absolute/path/node.json [--check]");
            return;
        }
        if (args.length < 1 || args.length > 2)
            throw new IllegalArgumentException("Expected node.json [--check]");
        var json = new ObjectMapper().findAndRegisterModules();
        if (args.length == 2 && (args[0].equals("--render") || args[0].equals("--firewall"))) {
            PreviewNodeTools.run(args[0], Path.of(args[1]), json);
            return;
        }
        if (args.length == 2 && !args[1].equals("--check"))
            throw new IllegalArgumentException("Expected --check");
        var config = PreviewNodeConfig.read(Path.of(args[0]), json);
        config.requireFiles();
        PreviewControlTls.secret(config.tokenFile());
        var tls = PreviewControlTls.server(config.keyStore(), config.keyStorePasswordFile());
        var pulls = new PreviewPullRegistry(config.workDir(), json);
        var transcodes =
                new PreviewTranscodeRegistry(config.workDir(), json, config.maxTranscodes());
        var media = new ZlmPreviewGateway(json, config, transcodes, new PreviewProcessInspector());
        var core =
                new LocalPreviewExecutor(
                        media,
                        config,
                        pulls,
                        transcodes,
                        new PreviewRecoveryCoordinator(pulls, media, config));
        core.budget(config.budget());
        var journal = new PreviewNodeJournal(config.workDir(), json);
        String configVersion = config.version(json);
        var service =
                new PreviewNodeService(
                        config.nodeId(),
                        configVersion,
                        config.maxSessions(),
                        config.maxTranscodes(),
                        core,
                        journal,
                        new PreviewNodeSourcePolicy(config.allowedTargets()),
                        json,
                        Clock.systemUTC(),
                        () -> {
                            if (!configVersion.equals(config.version(json)))
                                throw new com.streamfusion.platform.media.preview.PreviewFailure(
                                        "NODE_CONFIGURATION_CHANGED");
                            media.checkNodeRuntime(
                                    config.activeZlm().orElseThrow(),
                                    config.ffmpegExecutable(),
                                    config.mediaAddress(),
                                    com.streamfusion.platform.media.preview.PreviewDeadline.after(
                                            java.time.Duration.ofSeconds(3)));
                        },
                        config.budget());
        if (args.length == 2) {
            config.activeZlm();
            System.out.println(
                    "Preview node configuration and TLS identity valid: " + config.nodeId());
            journal.close();
            pulls.close();
            transcodes.close();
            return;
        }
        var server =
                new PreviewNodeServer(
                        new InetSocketAddress(config.bindHost(), config.controlPort()),
                        tls,
                        config.tokenFile(),
                        service,
                        json);
        var maintenance =
                Executors.newSingleThreadScheduledExecutor(
                        r -> new Thread(r, "preview-node-expiry"));
        maintenance.scheduleWithFixedDelay(
                () -> {
                    MDC.put("traceId", "node-" + config.nodeId());
                    try {
                        service.tick();
                    } catch (Exception failure) {
                        LoggerFactory.getLogger(PreviewNodeApplication.class)
                                .warn(
                                        "Preview node reconciliation pending nodeId={} reasonCode={}",
                                        config.nodeId(),
                                        failure
                                                        instanceof
                                                        com.streamfusion.platform.media.preview
                                                                        .PreviewFailure
                                                                known
                                                ? known.reason()
                                                : "NODE_RECOVERY_FAILED");
                    } finally {
                        MDC.remove("traceId");
                    }
                },
                0,
                5,
                TimeUnit.SECONDS);
        Runtime.getRuntime()
                .addShutdownHook(
                        new Thread(
                                () -> {
                                    server.close();
                                    maintenance.shutdownNow();
                                    try {
                                        maintenance.awaitTermination(15, TimeUnit.SECONDS);
                                    } catch (InterruptedException ex) {
                                        Thread.currentThread().interrupt();
                                    }
                                    // Resource journals remain for exact recovery; process exit
                                    // does not imply media exit.
                                    journal.close();
                                    pulls.close();
                                    transcodes.close();
                                },
                                "preview-node-shutdown"));
        server.start();
        System.out.println(
                "Preview node listening nodeId=" + config.nodeId() + " port=" + server.port());
    }
}
