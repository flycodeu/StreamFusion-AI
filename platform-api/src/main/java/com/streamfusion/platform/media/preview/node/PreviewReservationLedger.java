package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewPlaybackPlanner;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import java.util.List;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Repository;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/**
 * Short transactions only. The scheduler serializes mutations; SQL locks protect budget updates.
 */
@Repository
public class PreviewReservationLedger {
    public record Source(
            long profileId,
            String connectionKey,
            String nodeId,
            String configVersion,
            String codec) {}

    private final JdbcTemplate sql;
    private final PreviewNodeRepository nodes;
    private final TransactionTemplate transactions;

    public PreviewReservationLedger(
            JdbcTemplate sql,
            PreviewNodeRepository nodes,
            PlatformTransactionManager transactions) {
        this.sql = sql;
        this.nodes = nodes;
        this.transactions = new TransactionTemplate(transactions);
    }

    public Source source(long profile) {
        return sql
                .query(
                        "SELECT * FROM preview_source WHERE profile_id=?",
                        (r, i) ->
                                new Source(
                                        profile,
                                        r.getString("connection_key"),
                                        r.getString("node_id"),
                                        r.getString("config_version"),
                                        r.getString("source_codec")),
                        profile)
                .stream()
                .findFirst()
                .orElse(null);
    }

    public PreviewBudget.Usage usage(String node) {
        int sources = count("SELECT COUNT(*) FROM preview_source WHERE node_id=?", node);
        int viewers = count("SELECT COUNT(*) FROM preview_viewer WHERE node_id=?", node);
        int starting =
                count(
                        "SELECT COALESCE(SUM(is_starting),0) FROM preview_viewer WHERE node_id=?",
                        node);
        int transcodes =
                count(
                        "SELECT COALESCE(SUM(o.transcode_units),0) FROM preview_output o JOIN preview_source s ON s.profile_id=o.profile_id WHERE s.node_id=?",
                        node);
        long ingress =
                sql.queryForObject(
                        "SELECT COALESCE(SUM(ingress_kbps),0) FROM preview_source WHERE node_id=?",
                        Long.class,
                        node);
        long egress =
                sql.queryForObject(
                        "SELECT COALESCE(SUM(egress_kbps),0) FROM preview_viewer WHERE node_id=?",
                        Long.class,
                        node);
        return new PreviewBudget.Usage(sources, viewers, transcodes, starting, ingress, egress);
    }

    private int count(String query, Object... args) {
        return sql.queryForObject(query, Integer.class, args);
    }

    public boolean hasUnresolvedOperations(String node) {
        return count(
                        "SELECT COUNT(*) FROM preview_operation WHERE node_id=? AND state IN ('PENDING','UNKNOWN')",
                        node)
                != 0;
    }

    public void reserve(
            String id,
            PreviewExecutor.SourceKey key,
            List<String> codecs,
            PreviewNodeRepository.Node candidate,
            String boot) {
        transactions.executeWithoutResult(
                status -> {
                    // Profile lock -> source -> node -> output/viewer. No network call in this
                    // transaction.
                    sql.queryForList(
                            "SELECT id FROM camera_stream_profile WHERE id=? FOR UPDATE",
                            key.profileId());
                    sql.queryForList(
                            "SELECT profile_id FROM preview_source WHERE profile_id=? FOR UPDATE",
                            key.profileId());
                    sql.queryForList(
                            "SELECT id FROM preview_node WHERE node_id=? FOR UPDATE",
                            candidate.nodeId());
                    var node = nodes.node(candidate.nodeId());
                    if (node == null
                            || node.version() != candidate.version()
                            || !node.mode().equals("REMOTE"))
                        throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
                    var old = source(key.profileId());
                    if (old != null
                            && (!old.connectionKey().equals(key.connectionKey())
                                    || !old.configVersion().equals(node.configVersion())))
                        throw new PreviewFailure("SOURCE_GENERATION_PENDING");
                    if (old != null && !old.nodeId().equals(node.nodeId()))
                        throw new PreviewFailure("SOURCE_ASSIGNMENT_CONFLICT");
                    if (old != null
                            && count(
                                            "SELECT COUNT(*) FROM preview_viewer WHERE profile_id=? AND state='CLOSING'",
                                            key.profileId())
                                    > 0) throw new PreviewFailure("CLEANUP_PENDING");
                    boolean mayTranscode =
                            old == null
                                    || old.codec() == null
                                    || PreviewPlaybackPlanner.decide(old.codec(), codecs)
                                            == PreviewPlaybackPlanner.Decision.TRANSCODE_H264;
                    // A direct-only node may probe a new source, but its media core forbids actual
                    // transcoding.
                    boolean transcode = mayTranscode && node.budget().maxTranscodes() > 0;
                    String output = transcode ? "h264:" + node.configVersion() : "direct";
                    boolean newOutput =
                            count(
                                            "SELECT COUNT(*) FROM preview_output WHERE profile_id=? AND output_key=?",
                                            key.profileId(),
                                            output)
                                    == 0;
                    node.budget().admit(usage(node.nodeId()), old == null, transcode && newOutput);
                    if (old == null)
                        sql.update(
                                "INSERT INTO preview_source(profile_id,connection_key,node_id,config_version,ingress_kbps) VALUES(?,?,?,?,?)",
                                key.profileId(),
                                key.connectionKey(),
                                node.nodeId(),
                                node.configVersion(),
                                node.budget().ingressPerSource());
                    if (newOutput)
                        sql.update(
                                "INSERT INTO preview_output(profile_id,output_key,transcode_units) VALUES(?,?,?)",
                                key.profileId(),
                                output,
                                transcode ? 1 : 0);
                    nodes.prepare(
                            new PreviewNodeRepository.Viewer(
                                    id, node.nodeId(), boot, node.configVersion(), "RESERVED"));
                    sql.update(
                            "UPDATE preview_viewer SET profile_id=?,connection_key=?,output_key=?,egress_kbps=? WHERE viewer_id=?",
                            key.profileId(),
                            key.connectionKey(),
                            output,
                            node.budget().egressPerViewer(),
                            id);
                });
    }

    public void ready(String id, PreviewExecutor.Playback playback) {
        transactions.executeWithoutResult(
                status -> {
                    var rows =
                            sql.queryForList(
                                    "SELECT profile_id,node_id FROM preview_viewer WHERE viewer_id=?",
                                    id);
                    if (rows.isEmpty()) throw new PreviewFailure("LEASE_EXPIRED");
                    long profile = ((Number) rows.getFirst().get("profile_id")).longValue();
                    sql.queryForList(
                            "SELECT id FROM camera_stream_profile WHERE id=? FOR UPDATE", profile);
                    sql.queryForList(
                            "SELECT profile_id FROM preview_source WHERE profile_id=? FOR UPDATE",
                            profile);
                    sql.queryForList(
                            "SELECT id FROM preview_node WHERE node_id=? FOR UPDATE",
                            rows.getFirst().get("node_id"));
                    if (sql.queryForList(
                                    "SELECT viewer_id FROM preview_viewer WHERE viewer_id=? FOR UPDATE",
                                    id)
                            .isEmpty()) throw new PreviewFailure("LEASE_EXPIRED");
                    sql.update(
                            "UPDATE preview_source SET source_codec=? WHERE profile_id=?",
                            playback.sourceCodec(),
                            profile);
                    if (!playback.transcoded()) {
                        if (count(
                                        "SELECT COUNT(*) FROM preview_output WHERE profile_id=? AND output_key='direct'",
                                        profile)
                                == 0)
                            sql.update(
                                    "INSERT INTO preview_output(profile_id,output_key,transcode_units) VALUES(?,'direct',0)",
                                    profile);
                        sql.update(
                                "UPDATE preview_viewer SET output_key='direct' WHERE viewer_id=?",
                                id);
                        pruneOutputs(profile);
                    }
                    sql.update(
                            "UPDATE preview_viewer SET state='READY',is_starting=0 WHERE viewer_id=?",
                            id);
                });
    }

    public void closed(String id) {
        transactions.executeWithoutResult(
                status -> {
                    var profiles =
                            sql.queryForList(
                                    "SELECT profile_id FROM preview_viewer WHERE viewer_id=?",
                                    Long.class,
                                    id);
                    if (profiles.isEmpty()) return;
                    Long profile = profiles.getFirst();
                    if (profile != null) {
                        sql.queryForList(
                                "SELECT id FROM camera_stream_profile WHERE id=? FOR UPDATE",
                                profile);
                        sql.queryForList(
                                "SELECT profile_id FROM preview_source WHERE profile_id=? FOR UPDATE",
                                profile);
                    }
                    var viewer = nodes.viewer(id);
                    if (viewer == null) return;
                    sql.queryForList(
                            "SELECT id FROM preview_node WHERE node_id=? FOR UPDATE",
                            viewer.nodeId());
                    nodes.resolved(id);
                    nodes.closed(id);
                    if (profile != null) {
                        pruneOutputs(profile);
                        if (count("SELECT COUNT(*) FROM preview_viewer WHERE profile_id=?", profile)
                                == 0)
                            sql.update("DELETE FROM preview_source WHERE profile_id=?", profile);
                    }
                });
    }

    private void pruneOutputs(long profile) {
        sql.update(
                "DELETE FROM preview_output WHERE profile_id=? AND output_key NOT IN (SELECT output_key FROM preview_viewer WHERE profile_id=? AND output_key IS NOT NULL)",
                profile,
                profile);
    }
}
