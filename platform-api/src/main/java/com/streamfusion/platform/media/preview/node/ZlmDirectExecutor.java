package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Component;

/** Direct H264-only executor. Every remote mutation has a durable viewer and exact stream tuple. */
@Component
public final class ZlmDirectExecutor implements PreviewExecutor {
    public static final String CODEC_FALLBACK = "DIRECT_CODEC_FALLBACK";
    private final PreviewNodeRepository nodes;
    private final ZlmDirectGateway gateway;
    private final JdbcTemplate sql;
    private final Map<String, CompletableFuture<Void>> openings = new ConcurrentHashMap<>();
    private final Map<String, ZlmDirectGateway.Stream> streams = new ConcurrentHashMap<>();
    private final java.util.Set<String> current = ConcurrentHashMap.newKeySet();

    public ZlmDirectExecutor(
            PreviewNodeRepository nodes, ZlmDirectGateway gateway, JdbcTemplate sql) {
        this.nodes = nodes;
        this.gateway = gateway;
        this.sql = sql;
    }

    public void requireAvailable(PreviewNodeRepository.Node node, PreviewDeadline deadline) {
        if (node.zlm() == null) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        gateway.requireReady(node.zlm(), deadline);
    }

    public void prepared(String id) {
        current.add(id);
    }

    @Override
    public void requireRecovered() {
        // Recovery is scoped by scheduler to the original node.
    }

    @Override
    public void prepare(String id) {
        throw new PreviewFailure("SOURCE_REQUIRED");
    }

    @Override
    public void attachSource(
            String id,
            SourceKey source,
            Supplier<URI> resolver,
            Runnable checkpoint,
            PreviewDeadline deadline) {
        var node = requireNode(id);
        var stream = tuple(node, source);
        streams.put(id, stream);
        checkpoint.run();
        var opening = new CompletableFuture<Void>();
        var existing = openings.putIfAbsent(key(node, stream), opening);
        if (existing == null) {
            try {
                // Another READY viewer uses the same source; no duplicate remote pull.
                if (!nodes.canReuseSource(id)) {
                    URI uri = resolver.get();
                    checkpoint.run();
                    if (uri != null && List.of("http", "https").contains(uri.getScheme()))
                        throw new PreviewFailure("HTTP_MEDIA_REQUIRES_LOCAL_NODE");
                    if (uri == null
                            || uri.getScheme() == null
                            || !List.of("rtsp", "rtsps", "rtmp")
                                    .contains(uri.getScheme().toLowerCase(java.util.Locale.ROOT)))
                        throw new PreviewFailure("SOURCE_UNSUPPORTED");
                    String operation = opening(node, id, stream, deadline);
                    gateway.open(node.zlm(), stream, uri, deadline);
                    sql.update(
                            "UPDATE preview_operation SET state='SUCCEEDED' WHERE operation_id=?",
                            operation);
                }
                opening.complete(null);
            } catch (RuntimeException failure) {
                opening.completeExceptionally(failure);
                throw failure;
            }
        } else {
            try {
                existing.get(deadline.remaining().toNanos(), TimeUnit.NANOSECONDS);
            } catch (InterruptedException ex) {
                Thread.currentThread().interrupt();
                throw new PreviewFailure("CANCELLED");
            } catch (java.util.concurrent.TimeoutException ex) {
                throw new PreviewFailure("PREVIEW_PENDING");
            } catch (ExecutionException ex) {
                if (ex.getCause() instanceof PreviewFailure failure) throw failure;
                throw new PreviewFailure("ZLM_UNAVAILABLE");
            }
        }
        checkpoint.run();
        var stats = awaitTracks(node, stream, checkpoint, deadline);
        if (!"H264".equals(stats.videoCodec())) throw new PreviewFailure(CODEC_FALLBACK);
    }

    @Override
    public Playback start(
            String id, List<String> codecs, Runnable checkpoint, PreviewDeadline deadline) {
        var node = requireNode(id);
        var source = requireStream(id, node);
        var stats = awaitTracks(node, source, checkpoint, deadline);
        if (!"H264".equals(stats.videoCodec())) throw new PreviewFailure(CODEC_FALLBACK);
        if (!codecs.contains("H264")) throw new PreviewFailure("CODEC_UNSUPPORTED");
        var output = viewerStream(id, source);
        checkpoint.run();
        String operation = opening(node, id, output, deadline);
        gateway.openViewer(node.zlm(), source, output, deadline);
        sql.update(
                "UPDATE preview_operation SET state='SUCCEEDED' WHERE operation_id=?", operation);
        stats = awaitTracks(node, output, checkpoint, deadline);
        if (!"H264".equals(stats.videoCodec())) throw new PreviewFailure("CODEC_UNSUPPORTED");
        nodes.state(id, "READY");
        return new Playback("H264", false, "H264");
    }

    private PreviewModels.Stats awaitTracks(
            PreviewNodeRepository.Node node,
            ZlmDirectGateway.Stream stream,
            Runnable checkpoint,
            PreviewDeadline deadline) {
        for (int i = 0; i < 4; i++) {
            checkpoint.run();
            var stats = gateway.stats(node.zlm(), stream, deadline);
            checkpoint.run();
            if ("READY".equals(stats.state()) && stats.videoCodec() != null) return stats;
            if (i < 3) {
                try {
                    TimeUnit.NANOSECONDS.sleep(
                            Math.min(
                                    TimeUnit.MILLISECONDS.toNanos(200),
                                    deadline.remaining().toNanos()));
                } catch (InterruptedException ex) {
                    Thread.currentThread().interrupt();
                    throw new PreviewFailure("CANCELLED");
                }
            }
        }
        throw new PreviewFailure("MEDIA_NOT_READY");
    }

    @Override
    public void confirmReady(String id, PreviewDeadline deadline) {
        deadline.check();
    }

    @Override
    public void renew(String id, PreviewDeadline deadline) {
        var node = requireNode(id);
        var source = requireStream(id, node);
        if (!gateway.exists(node.zlm(), source, deadline)
                || !gateway.exists(node.zlm(), viewerStream(id, source), deadline))
            throw new PreviewFailure("MEDIA_NOT_READY");
    }

    @Override
    public void requireCurrent(String id) {
        requireNode(id);
    }

    @Override
    public List<String> missingSourceViewers(String id, PreviewDeadline deadline) {
        var viewer = nodes.viewer(id);
        if (viewer == null || "RESERVED".equals(viewer.state())) return List.of();
        var node = requireNode(id);
        if (gateway.exists(node.zlm(), requireStream(id, node), deadline)) return List.of();
        var assignment = assignment(id);
        return sql.queryForList(
                "SELECT viewer_id FROM preview_viewer WHERE profile_id=? AND connection_key=? AND node_id=?",
                String.class,
                assignment.profileId(),
                assignment.connectionKey(),
                node.nodeId());
    }

    @Override
    public PreviewModels.Answer answer(String id, String sdp, PreviewDeadline deadline) {
        var node = requireNode(id);
        return gateway.answer(node.zlm(), viewerStream(id, requireStream(id, node)), sdp, deadline);
    }

    @Override
    public PreviewModels.Stats stats(String id, PreviewDeadline deadline) {
        var node = requireNode(id);
        return gateway.stats(node.zlm(), viewerStream(id, requireStream(id, node)), deadline);
    }

    @Override
    public void close(String id, PreviewDeadline deadline) {
        var viewer = nodes.viewer(id);
        if (viewer == null) {
            current.remove(id);
            streams.remove(id);
            return;
        }
        var node = requireNode(id);
        nodes.state(id, "CLOSING");
        var stream = requireStream(id, node);
        var output = viewerStream(id, stream);
        cleanup(node, output, deadline);
        var assignment = assignment(id);
        int others =
                sql.queryForObject(
                        "SELECT COUNT(*) FROM preview_viewer WHERE profile_id=? AND connection_key=? AND node_id=? AND viewer_id<>?",
                        Integer.class,
                        assignment.profileId(),
                        assignment.connectionKey(),
                        node.nodeId(),
                        id);
        if (others == 0) {
            cleanup(node, stream, deadline);
            openings.remove(key(node, stream));
        }
        streams.remove(id);
        current.remove(id);
    }

    @Override
    public int occupiedTranscodes() {
        return 0;
    }

    @Override
    public void reconcile(BooleanSupplier stopping) {}

    private PreviewNodeRepository.Node requireNode(String id) {
        var viewer = nodes.viewer(id);
        var node = viewer == null ? null : nodes.node(viewer.nodeId());
        if (node == null
                || node.zlm() == null
                || !viewer.configVersion().equals(node.configVersion()))
            throw new PreviewFailure("NODE_IDENTITY_CHANGED");
        return node;
    }

    private ZlmDirectGateway.Stream requireStream(String id, PreviewNodeRepository.Node node) {
        var inMemory = streams.get(id);
        if (inMemory != null) return inMemory;
        return tuple(node, assignment(id));
    }

    private SourceKey assignment(String id) {
        var rows =
                sql.queryForList(
                        "SELECT profile_id,connection_key FROM preview_viewer WHERE viewer_id=?",
                        id);
        if (rows.isEmpty()
                || rows.getFirst().get("profile_id") == null
                || rows.getFirst().get("connection_key") == null)
            throw new PreviewFailure("MEDIA_NOT_READY");
        return new SourceKey(
                ((Number) rows.getFirst().get("profile_id")).longValue(),
                (String) rows.getFirst().get("connection_key"));
    }

    private static ZlmDirectGateway.Stream tuple(PreviewNodeRepository.Node node, SourceKey key) {
        return new ZlmDirectGateway.Stream(
                "sf_preview_" + digest(node.nodeId()).substring(0, 12),
                digest(key.profileId() + "\n" + key.connectionKey() + "\n" + node.configVersion())
                        .substring(0, 32));
    }

    private static ZlmDirectGateway.Stream viewerStream(String id, ZlmDirectGateway.Stream source) {
        String normalized = id.replace("-", "");
        if (!normalized.matches("[a-f0-9]{32}")) throw new PreviewFailure("LEASE_EXPIRED");
        return new ZlmDirectGateway.Stream(source.app(), normalized);
    }

    private String opening(
            PreviewNodeRepository.Node node,
            String id,
            ZlmDirectGateway.Stream stream,
            PreviewDeadline deadline) {
        sql.update(
                "DELETE FROM preview_operation WHERE deadline_at<? AND state IN ('SUCCEEDED','FAILED','CLOSED')",
                System.currentTimeMillis() - 120_000);
        if (sql.queryForObject("SELECT COUNT(*) FROM preview_operation", Integer.class) >= 32768)
            throw new PreviewFailure("NODE_JOURNAL_FULL");
        String operation = UUID.randomUUID().toString();
        long notBefore = System.currentTimeMillis() + deadline.remaining().toMillis() + 30_000;
        sql.update(
                "INSERT INTO preview_operation(operation_id,node_id,viewer_id,action,command_digest,deadline_at,state) VALUES(?,?,?,?,?,?,'PENDING')",
                operation,
                node.nodeId(),
                id,
                "CREATE",
                digest(key(node, stream)),
                notBefore);
        return operation;
    }

    private void cleanup(
            PreviewNodeRepository.Node node,
            ZlmDirectGateway.Stream stream,
            PreviewDeadline deadline) {
        gateway.close(node.zlm(), stream, deadline);
        Long notBefore =
                sql.queryForObject(
                        "SELECT COALESCE(MAX(deadline_at),0) FROM preview_operation WHERE node_id=? AND command_digest=? AND action='CREATE' AND state IN ('PENDING','UNKNOWN')",
                        Long.class,
                        node.nodeId(),
                        digest(key(node, stream)));
        if (notBefore != null && System.currentTimeMillis() < notBefore)
            throw new PreviewFailure("CLEANUP_PENDING");
        sql.update(
                "UPDATE preview_operation SET state='CLOSED',reason_code='RESOURCE_CLOSED' WHERE node_id=? AND command_digest=? AND action='CREATE' AND state IN ('PENDING','UNKNOWN')",
                node.nodeId(),
                digest(key(node, stream)));
    }

    private static String key(PreviewNodeRepository.Node node, ZlmDirectGateway.Stream stream) {
        return node.nodeId() + ":" + stream.app() + ":" + stream.id();
    }

    private static String digest(String input) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance("SHA-256")
                                    .digest(input.getBytes(StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException impossible) {
            throw new IllegalStateException(impossible);
        }
    }
}
